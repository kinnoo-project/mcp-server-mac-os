// builtins_shortcuts.go implements the read-only half of the `shortcuts` domain
// (V8): enumerating the Shortcuts the user has authored on this Mac.
//
//   - list_shortcuts — what shortcuts exist? (shortcuts list --show-identifiers)
//
// The mutating half — run_shortcut — lives in mutate_shortcuts.go. Both share the
// pinned argv builders and the name-listing helper defined here.
//
// # Why the `shortcuts` binary is safe to invoke
//
// `shortcuts` is not on the registry deny list, but running an arbitrary shortcut
// IS the project's single most powerful capability (a shortcut is unbounded,
// user-authored automation). The safety split is by sub-command: this file only
// ever uses the read-only `list` verb, and the argv is fixed (no model input at
// all), so listing can never trigger, create, or delete a shortcut. The dangerous
// `run` verb is confined to the staged mutator, which is pinned HIGH-risk and
// irreversible in the security-invariants test.
//
// # Injection posture
//
// list_shortcuts takes no parameters, so it has no injection surface: the argv is
// the constant {"list", "--show-identifiers"}. The mutator side guards its one
// model-controlled value (the shortcut name) with a dash-leading rejection plus a
// stage-time existence check; see mutate_shortcuts.go.
package engine

import (
	"context"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// shortcutsListArgs is the pinned argv for list_shortcuts: the read-only `list`
// verb with `--show-identifiers` so each shortcut's stable UUID is shown next to
// its name. No model input rides in this vector. Kept as a pure function so tests
// (and the run_shortcut existence probe) can reason about the exact shape.
func shortcutsListArgs() []string { return []string{"list", "--show-identifiers"} }

// shortcutsListNamesArgs is the pinned argv for the plain name listing used by
// the run_shortcut existence check: `shortcuts list` with no `--show-identifiers`,
// so each output line is exactly a shortcut name and can be matched verbatim
// against the requested name.
func shortcutsListNamesArgs() []string { return []string{"list"} }

// runListShortcuts enumerates the user's shortcuts via `shortcuts list
// --show-identifiers`. Fixed argv, no model input, no injection surface. An empty
// listing is reported as "no shortcuts" rather than as an error, since a user with
// no shortcuts is a normal state.
func runListShortcuts(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("shortcuts")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, shortcutsListArgs()...)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		// A non-zero exit with a message on stderr (e.g. Shortcuts not available)
		// should surface that message; otherwise it is simply an empty library.
		if detail := strings.TrimSpace(res.Stderr); detail != "" {
			return "No shortcuts were listed: " + detail, nil
		}
		return "This Mac has no shortcuts.", nil
	}
	return "Your shortcuts (name and identifier):\n\n" + compactOutput(out), nil
}

// listShortcutNames runs the plain `shortcuts list` and returns the set of
// shortcut names, one per output line. It is the stage-time existence oracle for
// run_shortcut: staging a run against a name absent from this set is refused so
// the preview can only ever name a real shortcut. Blank lines are skipped; names
// are compared after trimming surrounding whitespace.
func listShortcutNames(ctx context.Context) (map[string]bool, error) {
	bin, err := policy.ResolveBinary("shortcuts")
	if err != nil {
		return nil, err
	}
	res, err := runCommand(ctx, bin, shortcutsListNamesArgs()...)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, line := range strings.Split(res.Stdout, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names[name] = true
		}
	}
	return names, nil
}
