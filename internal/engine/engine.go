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
// engine actually knows how to run. This is the fail-fast counterpart to the
// registry's structural validation: because builders live in this package, the
// "is this builder real?" check belongs here, keeping the registry free of any
// dependency on the engine. Returns the first offending capability.
func (e *Engine) ValidateBuilders(caps []registry.Capability) error {
	for _, c := range caps {
		if _, ok := lookupBuilder(c.Builder); !ok {
			return fmt.Errorf("engine: capability %q references unknown builder %q", c.Name, c.Builder)
		}
	}
	return nil
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
