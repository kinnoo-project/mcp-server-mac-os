// engine.go is the orchestration seam that turns a validated capability plus a
// client-supplied parameter object into rendered command output.
//
// Run is the single execution pipeline shared by every read-only capability:
//
//	normalize params -> select builder -> assemble argv -> resolve binary -> exec -> format
//
// Keeping this pipeline in one place means cross-cutting guarantees (input
// validation, the policy trust check, output compaction, context cancellation)
// are enforced uniformly for all capabilities rather than re-implemented per
// tool. The engine depends only on the registry's data types and the policy
// trust boundary — never the other way around.
package engine

import (
	"context"
	"fmt"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// Engine executes capabilities. It is stateless and safe for concurrent use; a
// single instance is shared across all requests.
type Engine struct{}

// New constructs an Engine.
func New() *Engine { return &Engine{} }

// ValidateBuilders checks, at startup, that every capability names a builder the
// engine actually knows how to run — an argv builder (subprocess), a builtin
// (answered in-process), or a mutator (staged for a two-phase mutation). This is
// the fail-fast counterpart to the registry's structural validation: because
// builders live in this package, the "is this builder real?" check belongs here,
// keeping the registry free of any dependency on the engine. It also checks
// AcceptsStdin's precondition (see registry.Capability's doc): only a
// read-only, argv-builder-backed capability may set it, since a builtin has no
// subprocess to wire stdin to and a mutator doesn't run in one step at all.
// Returns the first offending capability.
func (e *Engine) ValidateBuilders(caps []registry.Capability) error {
	for _, c := range caps {
		if !builderExists(c.Builder) {
			return fmt.Errorf("engine: capability %q references unknown builder %q", c.Name, c.Builder)
		}
		if c.AcceptsStdin {
			if c.Reversibility != registry.ReadOnly {
				return fmt.Errorf("engine: capability %q sets accepts_stdin but is not read-only", c.Name)
			}
			if _, ok := lookupBuilder(c.Builder); !ok {
				return fmt.Errorf("engine: capability %q sets accepts_stdin but its builder %q is not an argv builder (builtins/mutators have no stdin to wire)", c.Name, c.Builder)
			}
		}
	}
	return nil
}

// builderExists reports whether a builder name resolves to an argv builder, a
// builtin, or a mutator.
func builderExists(name string) bool {
	if _, ok := builders[name]; ok {
		return true
	}
	if _, ok := builtins[name]; ok {
		return true
	}
	_, ok := mutators[name]
	return ok
}

// Run executes a single capability with the given raw (client-supplied)
// parameters and returns the rendered output text.
//
// The steps are deliberately ordered so that nothing with side effects happens
// until all validation has passed: parameters are normalized and the argv is
// fully assembled before the binary is resolved or any subprocess is spawned.
// ctx bounds the child process so a cancelled request kills the command.
func (e *Engine) Run(ctx context.Context, c registry.Capability, raw map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	normalized, err := normalizeParams(c, raw)
	if err != nil {
		return "", err
	}

	// An AcceptsStdin capability (wc, grep, sort, head, ...) reads its input
	// from a positional file argument OR from stdin — but a standalone Run
	// call never wires up stdin (only RunPipeline does, from a prior stage's
	// output). Without this guard, omitting the file argument here would
	// launch a subprocess that blocks forever waiting for input that will
	// never arrive, until the request's context eventually cancels it. Refuse
	// up front instead, with a message pointing at the actual fix.
	if c.AcceptsStdin && missingPositionalInput(c, normalized) {
		return "", fmt.Errorf("%s: requires its input parameter when called directly (there is no piped input to read from outside a pipeline)", c.Name)
	}

	// Builtin capabilities are answered in-process and never touch the policy
	// trust check or a subprocess (see builtins.go).
	if builtin, ok := lookupBuiltin(c.Builder); ok {
		return builtin(ctx, c, normalized)
	}

	build, ok := lookupBuilder(c.Builder)
	if !ok {
		return "", fmt.Errorf("engine: capability %q references unknown builder %q", c.Name, c.Builder)
	}
	args, err := build(c, normalized)
	if err != nil {
		return "", err
	}

	bin, err := policy.ResolveBinary(c.Binary)
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, args...)
	if err != nil {
		return "", err
	}
	return formatRunResult(res), nil
}
