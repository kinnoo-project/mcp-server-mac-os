// mutate_shortcuts_test.go covers the shortcuts domain's one mutator,
// run_shortcut: the forward argv pin (with and without an input file), the "--"
// terminator that keeps a dash-leading name a positional, the name/input
// injection regressions, and the "no auto-undo" contract. The stage-time
// existence check is exercised through a name that cannot exist, which must be
// refused rather than staged.
package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestShortcutsRunArgvBuilder pins the forward argument vectors. Without an input
// the name rides after "--"; with one, the validated input path is an `-i` flag
// value placed BEFORE the terminator so ArgumentParser recognises it, and the
// name still ends up as the final positional.
func TestShortcutsRunArgvBuilder(t *testing.T) {
	if got, want := strings.Join(shortcutsRunArgs("Morning Routine", ""), "\x1f"), strings.Join([]string{"run", "--", "Morning Routine"}, "\x1f"); got != want {
		t.Errorf("shortcutsRunArgs(no input) = %v, want [run -- Morning Routine]", shortcutsRunArgs("Morning Routine", ""))
	}
	withInput := shortcutsRunArgs("Morning Routine", "/tmp/in.txt")
	want := []string{"run", "-i", "/tmp/in.txt", "--", "Morning Routine"}
	if strings.Join(withInput, "\x1f") != strings.Join(want, "\x1f") {
		t.Errorf("shortcutsRunArgs(with input) = %v, want %v", withInput, want)
	}
	// The list builders must stay read-only and fixed.
	if got := strings.Join(shortcutsListArgs(), " "); got != "list --show-identifiers" {
		t.Errorf("shortcutsListArgs = %q, want \"list --show-identifiers\"", got)
	}
	if got := strings.Join(shortcutsListNamesArgs(), " "); got != "list" {
		t.Errorf("shortcutsListNamesArgs = %q, want \"list\"", got)
	}
}

// TestShortcutsRun_TerminatorPlacesNameLast is the "-e"-lands-as-data regression
// required by CLAUDE.md §4, at the argv layer: whatever the name, it must appear
// immediately after the "--" terminator so `shortcuts` reads it as the positional
// shortcut name and never as one of its own flags.
func TestShortcutsRun_TerminatorPlacesNameLast(t *testing.T) {
	for _, name := range []string{"Morning Routine", "-e", "--output-path", "-i"} {
		argv := shortcutsRunArgs(name, "")
		term := indexOf(argv, "--")
		if term < 0 {
			t.Errorf("name %q: argv has no '--' terminator: %v", name, argv)
			continue
		}
		if argv[len(argv)-1] != name {
			t.Errorf("name %q: expected it as the final positional after '--', got argv %v", name, argv)
		}
		if !appearsAfter(argv, name, term) {
			t.Errorf("name %q: does not appear after the '--' terminator: %v", name, argv)
		}
	}
}

// TestValidateShortcutName_RejectsHostile confirms the up-front (belt-and-braces)
// name guard refuses empty, dash-leading, and control-laden values before they
// ever reach argv or the existence probe.
func TestValidateShortcutName_RejectsHostile(t *testing.T) {
	// Covers empty/whitespace-only, dash-leading, and the FULL ASCII control range
	// (NUL/LF/CR plus tab, ESC, and DEL) — the last three would have slipped
	// through the original NUL/LF/CR-only guard.
	for _, bad := range []string{
		"", "   ", "-e", "-rf", "--flood", "-",
		"bad\nname", "bad\x00name", "bad\tname", "bad\x1bname", "bad\x7fname",
	} {
		if _, err := validateShortcutName("run_shortcut", bad); err == nil {
			t.Errorf("validateShortcutName(%q): expected rejection, got nil", bad)
		}
	}
	// A normal name (internal spaces are fine — users name shortcuts freely) is
	// accepted unchanged.
	if got, err := validateShortcutName("run_shortcut", "Morning Routine"); err != nil || got != "Morning Routine" {
		t.Errorf("validateShortcutName(\"Morning Routine\") = %q, %v; want it accepted unchanged", got, err)
	}
	// Surrounding whitespace is TRIMMED (not rejected) so the returned value
	// matches how listShortcutNames trims each listed name — otherwise a padded
	// name would clear validation but fail the existence check confusingly.
	if got, err := validateShortcutName("run_shortcut", "  Morning Routine  "); err != nil || got != "Morning Routine" {
		t.Errorf("validateShortcutName(padded) = %q, %v; want trimmed \"Morning Routine\"", got, err)
	}
}

// TestShortcutsRun_HostileNameRejectedAtStage is the mutator-level injection
// regression: a dash-leading name is refused at stage time (before the existence
// probe or any argv), so it can never reach the `shortcuts` command.
func TestShortcutsRun_HostileNameRejectedAtStage(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	for _, hostile := range []string{"-e", "-rf", "--flood", "-", ""} {
		if _, err := stageRunShortcut(ctx, cap, map[string]any{"name": hostile}); err == nil {
			t.Errorf("stageRunShortcut(name=%q): expected rejection, got nil", hostile)
		}
	}
}

// TestShortcutsRun_HostileInputRejected confirms a dash-leading or nonexistent
// input file is refused at stage time (validateExistingOperand), before the
// existence probe, so a hostile input path can never become a `shortcuts -i`
// value.
func TestShortcutsRun_HostileInputRejected(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	// Dash-leading input path: rejected regardless of the (valid) name.
	if _, err := stageRunShortcut(ctx, cap, map[string]any{"name": "Morning Routine", "input": "-e"}); err == nil {
		t.Error("stageRunShortcut with input=\"-e\": expected rejection of a dash-leading input path, got nil")
	}
	// Nonexistent input file: rejected with a "does not exist" error.
	missing := filepath.Join(t.TempDir(), "no-such-input.txt")
	if _, err := stageRunShortcut(ctx, cap, map[string]any{"name": "Morning Routine", "input": missing}); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("stageRunShortcut with a missing input: expected a 'does not exist' error, got %v", err)
	}
}

// TestShortcutsRun_NonexistentShortcutRejected proves the stage-time existence
// check: staging a run of a shortcut that does not exist is refused rather than
// staged, so a human is never asked to confirm a no-op (or a near-miss name). The
// name uses a benign charset (so it clears validateShortcutName) but is chosen so
// it cannot match any real shortcut. Either outcome — "no such shortcut" or an
// inability to list shortcuts — is a stage error, which is what we assert.
func TestShortcutsRun_NonexistentShortcutRejected(t *testing.T) {
	const fake = "mcp test nonexistent shortcut 0e5a1c7f no match"
	// Sanity: the name itself is acceptable, so any error must come from the
	// existence check, not the up-front guard.
	if _, err := validateShortcutName("run_shortcut", fake); err != nil {
		t.Fatalf("test name unexpectedly rejected by validateShortcutName: %v", err)
	}
	if _, err := stageRunShortcut(context.Background(), registry.Capability{}, map[string]any{"name": fake}); err == nil {
		t.Errorf("stageRunShortcut(%q): expected a stage rejection (no such shortcut), got nil", fake)
	}
}

// TestShortcutsRun_NoInverse checks the irreversibility contract at the plan
// level: when a run IS staged, it carries no auto-undo and its preview says so.
// It only asserts anything when a real shortcut exists on the test machine, so it
// never depends on a fixture being installed.
func TestShortcutsRun_NoInverse(t *testing.T) {
	names, err := listShortcutNames(context.Background())
	if err != nil || len(names) == 0 {
		t.Skip("no shortcuts available on this machine to stage a run against")
	}
	var some string
	for n := range names {
		if _, verr := validateShortcutName("run_shortcut", n); verr == nil {
			some = n
			break
		}
	}
	if some == "" {
		t.Skip("no shortcut with a guard-clearing name available")
	}
	plan, err := stageRunShortcut(context.Background(), registry.Capability{}, map[string]any{"name": some})
	if err != nil {
		t.Fatalf("stageRunShortcut(%q): %v", some, err)
	}
	if plan.Forward.Binary != "shortcuts" || plan.Forward.Args[0] != "run" || plan.Forward.Args[len(plan.Forward.Args)-1] != some {
		t.Errorf("forward = %s %v, want shortcuts run ... -- %q", plan.Forward.Binary, plan.Forward.Args, some)
	}
	if plan.Inverse != nil {
		t.Errorf("run_shortcut must have no auto-undo (Inverse nil), got %+v", plan.Inverse)
	}
	if !strings.Contains(plan.Preview, "CANNOT be undone") {
		t.Errorf("preview should state the run cannot be undone, got: %s", plan.Preview)
	}
}
