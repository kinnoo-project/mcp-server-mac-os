// mutate.go is the engine's mutation seam: the counterpart to the read-only Run
// pipeline for capabilities that CHANGE system state.
//
// # Why mutation is shaped differently from reads
//
// A read-only capability is a pure function of its parameters, so Run can
// validate, build argv, and execute in a single call. A mutation cannot: the
// only safe moment to show a human what will happen — and the only moment we can
// capture the prior state needed to reverse it — is AFTER validation but BEFORE
// execution. So mutation is split into two phases bridged by a token (see
// internal/transaction and .claude/rules/transactional-state.md):
//
//	Stage:  validate -> capture undo state -> assemble forward & inverse commands -> preview   (NO side effects)
//	commit: run the staged Forward command                                                     (the actual change)
//	undo:   run the staged Inverse command                                                     (the reversal)
//
// Crucially, BOTH the forward and inverse commands are fully resolved at stage
// time, so whatever prior state staging observed is baked into the inverse. The
// later commit/undo steps just run those pre-built commands and recompute
// nothing — which is what lets the server promise that "what executes is exactly
// what was staged and previewed."
//
// A "mutator" is the mutating analogue of the named argv builders in
// argbuild.go: declarative manifest data drives validation/risk/reversibility,
// while the inherently stateful logic (probing prior state, choosing the right
// inverse) lives in a small named Go function selected by Capability.Builder.
// Per-domain mutators live in their own files (mutate_filesystem.go,
// mutate_preferences.go, ...), mirroring how builders_filesystem.go separates
// the named argv builders from the generic machinery in argbuild.go. This file
// holds only the machinery shared by every mutator.
package engine

import (
	"context"
	"fmt"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// Command is a fully-tokenized, ready-to-run invocation: a bare binary name
// (resolved to a trusted absolute path through the policy layer at run time) and
// its explicit positional argument vector. It carries no shell string — argv is
// already split — which preserves the project's no-shell execution axiom across
// the stage/commit boundary.
type Command struct {
	// Binary is the bare utility name (e.g. "mkdir"); policy.ResolveBinary maps
	// it to a trusted absolute path before execution.
	Binary string
	// Args is the pre-split argument vector passed to the binary verbatim.
	Args []string
}

// StagedPlan is the product of staging a mutating capability: everything needed
// to commit the change and, when possible, to reverse it, plus human-readable
// text describing the effect.
type StagedPlan struct {
	// Preview is a plain-language description of what committing will do and what
	// undo will do, suitable for showing a user before they approve.
	Preview string
	// Forward is the command that performs the mutation when committed.
	Forward Command
	// Inverse is the compensating command that reverses the mutation, or nil when
	// the operation is irreversible (in which case there is no undo to offer).
	Inverse *Command
}

// Mutator stages one mutating capability. Like an ArgBuilder it receives the
// already-normalized parameter map, but instead of returning argv it performs
// any read-only probing required to capture prior state and returns a complete
// StagedPlan. A Mutator MUST NOT change system state — that is the commit step's
// job.
type Mutator func(ctx context.Context, c registry.Capability, in map[string]any) (*StagedPlan, error)

// mutators maps a capability's Builder name to its mutator implementation. It is
// disjoint from the read-only `builders` and `builtins` maps: a capability is
// read-only (run via Run) or mutating (staged via Stage), never both.
var mutators = map[string]Mutator{
	"mkdir":             stageMkdir,
	"write_setting":     stageWriteSetting,
	"send_mail":         stageSendMail,
	"add_event":         stageAddEvent,
	"modify_event":      stageModifyEvent,
	"delete_event":      stageDeleteEvent,
	"add_reminder":      stageAddReminder,
	"modify_reminder":   stageModifyReminder,
	"complete_reminder": stageCompleteReminder,
	"delete_reminder":   stageDeleteReminder,
	"call":              stageCall,
	"send_message":      stageSendMessage,
	"create_note":       stageCreateNote,
	"append_to_note":    stageAppendToNote,
	"open_application":  stageOpenApplication,
	"open_file":         stageOpenFile,
	"focus_application": stageFocusApplication,
	"quit_application":  stageQuitApplication,
	"print_file":        stagePrintFile,
	"print_test_page":   stagePrintTestPage,
	"open_settings":     stageOpenSettings,
}

// lookupMutator returns the mutator for a builder name and whether one exists.
func lookupMutator(name string) (Mutator, bool) {
	m, ok := mutators[name]
	return m, ok
}

// Stage validates a mutating capability's parameters and produces a StagedPlan
// WITHOUT performing the mutation. It is the mutating counterpart to Run;
// callers must route read-only capabilities through Run and mutating ones
// through Stage (the server decides based on Capability.Reversibility).
//
// As with Run, ctx is honoured up front so a cancelled request stages nothing,
// and parameters are validated/normalized before the mutator ever sees them.
func (e *Engine) Stage(ctx context.Context, c registry.Capability, raw map[string]any) (*StagedPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := normalizeParams(c, raw)
	if err != nil {
		return nil, err
	}
	mutate, ok := lookupMutator(c.Builder)
	if !ok {
		return nil, fmt.Errorf("engine: capability %q references unknown mutator %q", c.Name, c.Builder)
	}
	return mutate(ctx, c, normalized)
}

// RunCommand resolves and executes a single pre-built Command, returning its
// rendered output. It is the shared execution primitive behind both committing a
// staged plan (its Forward command) and undoing one (its Inverse command).
//
// Because the Command was assembled at stage time from validated input, this
// path performs no further parsing — it only enforces the same policy trust
// check and context binding that every subprocess in the engine obeys. Mutators
// themselves are plain functions (not Engine methods) and use the package-level
// runCommand/policy.ResolveBinary directly for any read-only probing they need
// before a plan is assembled (see mutate_preferences.go).
func (e *Engine) RunCommand(ctx context.Context, cmd Command) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	bin, err := policy.ResolveBinary(cmd.Binary)
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, cmd.Args...)
	if err != nil {
		return "", err
	}
	return formatRunResult(res), nil
}
