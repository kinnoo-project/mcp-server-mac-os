// pipeline.go composes several read-only, binary-backed capabilities into one
// server-side pipe chain: stage i's raw stdout becomes stage i+1's stdin,
// exactly the unix `a | b | c` idiom, with no shell involved (every stage is
// still launched via exec.CommandContext with its own validated, explicit
// argv — composing capabilities does not relax any of the project's no-shell
// or injection guardrails).
//
// # Why read-only, binary-backed stages only
//
// Builtins (pwd, largest_files) have no subprocess and so nothing to wire a
// pipe to. Mutators (mkdir, write_setting) don't run in one step at all —
// they stage, then commit, then optionally undo — so "pipe its output
// somewhere" doesn't have a coherent meaning yet. Restricting pipelines to
// read-only stages also means there is never anything to roll back: nothing
// mutates, so composition and the mutation-phase's transactional machinery
// stay orthogonal, exactly as decided when mutation was designed.
//
// # Why sequential and buffered, not a true concurrent OS pipe
//
// A true unix pipe runs every stage concurrently, streaming bytes through a
// kernel buffer so an arbitrarily large stream never sits fully in memory.
// This implementation instead runs each stage to completion, captures its
// stdout (capped — see maxPipelineStageBytes), and feeds that buffer as the
// next stage's stdin. That is simpler and exactly as correct at this
// project's scale (at most MaxPipelineStages stages, each capped well below
// what either Go or the model needs to worry about); true concurrent
// streaming would only start to matter for stages producing far more data
// than this server is ever asked to handle in one request.
package engine

import (
	"context"
	"fmt"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// MaxPipelineStages bounds how many capabilities one pipeline call may chain.
// The motivating idiom (du | sort | head) is 3 stages; 5 gives realistic
// headroom (e.g. find | grep | sort | head) while keeping any one call's
// worst-case latency and cost bounded.
const MaxPipelineStages = 5

// maxPipelineStageBytes caps each INTERMEDIATE stage's raw stdout. Unlike the
// model-facing 8 KB budget in executor.go's compactOutput (which exists
// because that text reaches the model's context window), intermediate
// pipeline data is never shown to the model — it only has to fit in this
// server's own memory, so the budget here is far more generous. Only the
// FINAL stage's output is compacted to the model-facing budget, exactly like
// a standalone Run call.
//
// A var, not a const, solely so a test can shrink it temporarily to exercise
// the cap without generating a real 1 MiB payload.
var maxPipelineStageBytes = 1 << 20 // 1 MiB

// PipelineStage is one already-resolved step of a pipeline: a concrete
// Capability (the caller — internal/server — has already done the
// registry.Lookup) plus its raw, client-supplied parameters.
type PipelineStage struct {
	Capability registry.Capability
	Params     map[string]any
}

// SupportsPipeline reports whether c may appear as a pipeline stage: it must
// be read-only (nothing to roll back, ever) and backed by an argv builder
// (excludes builtins, which have no subprocess to pipe, and mutators, which
// don't run in one step). Exported so the server layer can validate and
// explain a rejected stage before ever calling RunPipeline, and so the
// pipeline tool's description can list eligible capabilities without
// duplicating this logic.
func (e *Engine) SupportsPipeline(c registry.Capability) bool {
	if c.Reversibility != registry.ReadOnly {
		return false
	}
	_, ok := lookupBuilder(c.Builder)
	return ok
}

// RunPipeline runs stages in order, piping each stage's raw stdout into the
// next stage's stdin, and returns the final stage's output rendered exactly
// like a standalone Run call (formatted and compacted for the model). It
// performs no mutation and stages nothing — every stage here is read-only by
// construction (the caller must have already filtered through
// SupportsPipeline; RunPipeline re-checks defensively and refuses otherwise).
//
// A failing stage (non-zero exit, a build/resolve error, or exceeding the
// intermediate size cap) aborts the whole pipeline immediately with an error
// naming which stage and why. There is nothing to roll back: no stage in a
// valid pipeline can have mutated anything.
func (e *Engine) RunPipeline(ctx context.Context, stages []PipelineStage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(stages) == 0 {
		return "", fmt.Errorf("pipeline: at least one stage is required")
	}
	if len(stages) > MaxPipelineStages {
		return "", fmt.Errorf("pipeline: %d stages exceeds the maximum of %d", len(stages), MaxPipelineStages)
	}

	var stdin []byte // nil for stage 0: there is no upstream stage to supply it
	var final *runResult
	for i, stage := range stages {
		c := stage.Capability
		if !e.SupportsPipeline(c) {
			return "", fmt.Errorf("pipeline: stage %d (%s) cannot be used in a pipeline; only read-only, binary-backed capabilities are eligible", i+1, c.Name)
		}

		normalized, err := normalizeParams(c, stage.Params)
		if err != nil {
			return "", fmt.Errorf("pipeline: stage %d (%s): %w", i+1, c.Name, err)
		}
		if stdin == nil && c.AcceptsStdin && missingPositionalInput(c, normalized) {
			return "", fmt.Errorf("pipeline: stage %d (%s) needs its input parameter set; it is the first stage, so there is no piped input from a prior stage to supply it", i+1, c.Name)
		}

		build, _ := lookupBuilder(c.Builder) // SupportsPipeline already confirmed this exists
		args, err := build(c, normalized)
		if err != nil {
			return "", fmt.Errorf("pipeline: stage %d (%s): %w", i+1, c.Name, err)
		}

		bin, err := policy.ResolveBinary(c.Binary)
		if err != nil {
			return "", fmt.Errorf("pipeline: stage %d (%s): %w", i+1, c.Name, err)
		}
		res, err := runCommandWithStdin(ctx, bin, stdin, args...)
		if err != nil {
			return "", fmt.Errorf("pipeline: stage %d (%s): %w", i+1, c.Name, err)
		}
		if res.ExitCode != 0 {
			return "", fmt.Errorf("pipeline: stage %d (%s) exited %d: %s", i+1, c.Name, res.ExitCode, res.Stderr)
		}
		if len(res.Stdout) > maxPipelineStageBytes {
			return "", fmt.Errorf("pipeline: stage %d (%s) produced %d bytes, exceeding the %d-byte intermediate limit", i+1, c.Name, len(res.Stdout), maxPipelineStageBytes)
		}

		stdin = []byte(res.Stdout)
		final = res
	}
	return formatRunResult(final), nil
}

// missingPositionalInput reports whether c's positional-kind parameter (the
// one slot a piped stage's input would fill — every AcceptsStdin capability
// has exactly one) is absent or empty in the normalized parameter map. Used
// both here (to refuse a pipeline's first stage rather than hang) and by
// Run (to refuse a standalone call the same way) — see engine.go.
func missingPositionalInput(c registry.Capability, normalized map[string]any) bool {
	for _, p := range c.Params {
		if p.Arg.Kind != registry.ArgPositional {
			continue
		}
		val, present := normalized[p.Name]
		if !present {
			return true
		}
		if list, ok := val.([]string); ok && len(list) == 0 {
			return true
		}
		if s, ok := val.(string); ok && s == "" {
			return true
		}
	}
	return false
}
