// mutate_apps_test.go tests the application mutators' validation and command
// construction, including the option-injection regression mandated by CLAUDE.md
// §4: a flag-like application name must be refused, and a normal name must land
// as DATA after the osascript "--" terminator, never as a flag.
//
// SAFETY: no test executes a StagedPlan, so no app is ever launched, focused, or
// quit. stageOpenApplication's success path is not unit-tested as a whole because
// it shells out to `lsappinfo` to decide on an undo; only its pre-check
// validation is exercised here, plus the pure `lsappinfo list` matcher
// (lsappinfoListsApp) directly. The forward/inverse construction is otherwise
// covered through focus/quit, which take the same name through the same osascript
// path.
package engine

import (
	"context"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// dataAfterTerminator returns the argv element immediately following the "--"
// end-of-options marker in an osascript Command — i.e. the first value osascript
// will expose to the script as `on run argv`. It fails the test if there is no
// terminator or nothing after it.
func dataAfterTerminator(t *testing.T, c Command) string {
	t.Helper()
	if c.Binary != "osascript" {
		t.Fatalf("expected osascript command, got %q", c.Binary)
	}
	for i, a := range c.Args {
		if a == "--" {
			if i+1 >= len(c.Args) {
				t.Fatalf("nothing follows the -- terminator in %v", c.Args)
			}
			return c.Args[i+1]
		}
	}
	t.Fatalf("no -- terminator in %v", c.Args)
	return ""
}

func TestValidateAppName(t *testing.T) {
	// Valid names pass through trimmed.
	if name, err := validateAppName("op", map[string]any{"name": "  Safari  "}); err != nil || name != "Safari" {
		t.Errorf("validateAppName(Safari) = %q, %v; want Safari", name, err)
	}
	// Rejected: empty, leading dash (flag-like), control characters.
	for _, bad := range []any{"", "   ", "-e", "--", "Bad\tName", "Bad\nName"} {
		if _, err := validateAppName("op", map[string]any{"name": bad}); err == nil {
			t.Errorf("validateAppName(%q) should have failed", bad)
		}
	}
}

// TestStageFocus_NameIsData is the option-injection regression for the focus
// path: a normal name lands after "--", and the activate command is irreversible.
func TestStageFocus_NameIsData(t *testing.T) {
	plan, err := stageFocusApplication(context.Background(), focusCapability(t), map[string]any{"name": "Safari"})
	if err != nil {
		t.Fatalf("stageFocusApplication: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("focus_application must be irreversible: Inverse should be nil")
	}
	if got := dataAfterTerminator(t, plan.Forward); got != "Safari" {
		t.Errorf("app name landed as %q, want it as data after -- (Safari)", got)
	}
}

// TestStageFocus_RejectsFlagLikeName confirms a "-e" name is refused before any
// command is built — the value can never become an osascript flag.
func TestStageFocus_RejectsFlagLikeName(t *testing.T) {
	if _, err := stageFocusApplication(context.Background(), focusCapability(t), map[string]any{"name": "-e"}); err == nil {
		t.Error("a flag-like app name (-e) should be rejected")
	}
}

func TestStageQuit_StagedIrreversible(t *testing.T) {
	plan, err := stageQuitApplication(context.Background(), quitCapability(t), map[string]any{"name": "Safari"})
	if err != nil {
		t.Fatalf("stageQuitApplication: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("quit_application offers no automatic undo: Inverse should be nil")
	}
	if got := dataAfterTerminator(t, plan.Forward); got != "Safari" {
		t.Errorf("app name landed as %q, want it as data after -- (Safari)", got)
	}
}

// TestStageOpen_RejectsBadName exercises open_application's validation without
// reaching its live probe (the bad name errors out first).
func TestStageOpen_RejectsBadName(t *testing.T) {
	if _, err := stageOpenApplication(context.Background(), openCapability(t), map[string]any{"name": "-e"}); err == nil {
		t.Error("a flag-like app name (-e) should be rejected before launch")
	}
	if _, err := stageOpenApplication(context.Background(), openCapability(t), map[string]any{"name": ""}); err == nil {
		t.Error("an empty app name should be rejected")
	}
}

// sampleLsappinfo mimics `lsappinfo list` output: a numbered header line bearing
// the quoted display name, followed by indented detail lines including the bundle
// path. Two apps are listed to exercise both the display-name and bundle-basename
// match paths.
const sampleLsappinfo = ` 12) "Safari" ASN:0x0-0xa00a:
    bundleID="com.apple.Safari"
    bundle path="/Applications/Safari.app"
    executable path="/Applications/Safari.app/Contents/MacOS/Safari"

 13) "Google Chrome" ASN:0x0-0xb00b:
    bundleID="com.google.Chrome"
    bundle path="/Applications/Google Chrome.app"
`

func TestLsappinfoListsApp(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Safari", true},        // exact display name
		{"safari", true},        // case-insensitive, like `open -a`
		{"Safari.app", true},    // bundle-name spelling with extension
		{"Google Chrome", true}, // display name with a space
		{"Notes", false},        // not running
		{"Saf", false},          // partial must NOT match (avoid bogus undo)
		{"", true},              // empty → err toward "running"/no undo
	}
	for _, c := range cases {
		if got := lsappinfoListsApp(sampleLsappinfo, c.name); got != c.want {
			t.Errorf("lsappinfoListsApp(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func focusCapability(t *testing.T) registry.Capability {
	return lookupCapability(t, "focus_application")
}
func quitCapability(t *testing.T) registry.Capability { return lookupCapability(t, "quit_application") }
func openCapability(t *testing.T) registry.Capability { return lookupCapability(t, "open_application") }
