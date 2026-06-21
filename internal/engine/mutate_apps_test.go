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
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestOpenFileForward_PathIsDataAfterTerminator is the open_file option-injection
// regression for the `open` command: the file path must always land AFTER the "--"
// terminator so it can never be read as one of open's own flags.
func TestOpenFileForward_PathIsDataAfterTerminator(t *testing.T) {
	cmd := openFileForward("Preview", "/Users/me/Leah.png")
	if cmd.Binary != "open" {
		t.Fatalf("expected open command, got %q", cmd.Binary)
	}
	termAt := -1
	for i, a := range cmd.Args {
		if a == "--" {
			termAt = i
			break
		}
	}
	if termAt < 0 {
		t.Fatalf("no -- terminator in %v", cmd.Args)
	}
	if termAt+1 >= len(cmd.Args) || cmd.Args[termAt+1] != "/Users/me/Leah.png" {
		t.Errorf("file path must be the first value after --; got args %v", cmd.Args)
	}
	if cmd.Args[0] != "-a" || cmd.Args[1] != "Preview" {
		t.Errorf("app must be passed via -a; got args %v", cmd.Args)
	}

	// With no app, the default-handler form drops -a: open -- <file>.
	def := openFileForward("", "/Users/me/Leah.png")
	if got := def.Args; len(got) != 2 || got[0] != "--" || got[1] != "/Users/me/Leah.png" {
		t.Errorf("default-app form should be [-- <file>]; got %v", got)
	}
}

// TestStageOpenFile_Validation exercises the checks that short-circuit BEFORE any
// live probing (so the test never touches mdfind/plutil/mdimport): a missing file
// param, a flag-like path (the option-injection regression — a "-e" file is
// refused, never forwarded), a non-existent file, a directory, and a flag-like app.
// A MISSING app is intentionally absent here: it is valid (the default-app path).
func TestStageOpenFile_Validation(t *testing.T) {
	cap := lookupCapability(t, "open_file")
	dir := t.TempDir()
	real := filepath.Join(dir, "x.png")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		params map[string]any
	}{
		{"missing file", map[string]any{"app": "Preview"}},
		{"empty file", map[string]any{"file": "   ", "app": "Preview"}},
		{"flag-like file", map[string]any{"file": "-e", "app": "Preview"}},
		{"nonexistent file", map[string]any{"file": filepath.Join(dir, "nope.png"), "app": "Preview"}},
		{"directory", map[string]any{"file": dir, "app": "Preview"}},
		{"flag-like app", map[string]any{"file": real, "app": "-e"}},
	}
	for _, c := range cases {
		if _, err := stageOpenFile(context.Background(), cap, c.params); err == nil {
			t.Errorf("%s: expected an error, got nil", c.name)
		}
	}
}

// TestStageOpenFile_DefaultApp covers the no-app path end to end (it does NO
// probing, so it is hermetic): a valid file with no app stages `open -- <file>`,
// offers no undo, and previews the default-application intent.
func TestStageOpenFile_DefaultApp(t *testing.T) {
	cap := lookupCapability(t, "open_file")
	dir := t.TempDir()
	real := filepath.Join(dir, "x.png")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := stageOpenFile(context.Background(), cap, map[string]any{"file": real})
	if err != nil {
		t.Fatalf("stageOpenFile (default app): %v", err)
	}
	if plan.Inverse != nil {
		t.Error("default-app open offers no targeted undo: Inverse should be nil")
	}
	wantArgs := []string{"--", real}
	if plan.Forward.Binary != "open" || !reflect.DeepEqual(plan.Forward.Args, wantArgs) {
		t.Errorf("forward = %s %v, want open %v", plan.Forward.Binary, plan.Forward.Args, wantArgs)
	}
	if !strings.Contains(plan.Preview, "default application") {
		t.Errorf("preview should mention the default application: %q", plan.Preview)
	}
}

// TestStageOpenFile_ReadsAppParam is the regression guarding that the named-app
// branch reads the "app" parameter (not "name"): a flag-like app must fail with
// the leading-dash message, which only happens if the value was actually read and
// validated. A param-name mix-up would instead yield a generic "is required".
func TestStageOpenFile_ReadsAppParam(t *testing.T) {
	cap := lookupCapability(t, "open_file")
	dir := t.TempDir()
	real := filepath.Join(dir, "x.png")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := stageOpenFile(context.Background(), cap, map[string]any{"file": real, "app": "-e"})
	if err == nil || !strings.Contains(err.Error(), "must not begin with '-'") {
		t.Errorf("expected the leading-dash app error (proving the app param was read), got %v", err)
	}
}

// TestComposeOpenFilePreview checks that each support verdict produces the right
// shape of preview: a clean intent line when supported, and a leading ⚠️ warning
// (carrying the file name) when unsupported or uncertain.
func TestComposeOpenFilePreview(t *testing.T) {
	clause := "undo will quit it again."
	file := "/Users/me/Leah.png"

	supported := composeOpenFilePreview(file, "Preview", clause,
		fileSupport{Level: supportSupported, FileType: "public.png"})
	if strings.Contains(supported, "⚠️") {
		t.Errorf("supported preview must carry no warning: %q", supported)
	}
	if !strings.Contains(supported, "Open file /Users/me/Leah.png with \"Preview\".") {
		t.Errorf("supported preview missing the concrete intent sentence: %q", supported)
	}
	if !strings.HasSuffix(supported, "Proceed?") {
		t.Errorf("supported preview should end with a Proceed? call to action: %q", supported)
	}

	unsupported := composeOpenFilePreview(file, "Calculator", clause,
		fileSupport{Level: supportUnsupported, FileType: "public.png", Accepts: []string{".calc"}})
	if !strings.HasPrefix(unsupported, "⚠️") {
		t.Errorf("unsupported preview must lead with a warning: %q", unsupported)
	}
	if !strings.Contains(unsupported, "Leah.png") || !strings.Contains(unsupported, ".calc") {
		t.Errorf("unsupported preview should name the file and what the app accepts: %q", unsupported)
	}

	uncertain := composeOpenFilePreview(file, "Mystery", clause,
		fileSupport{Level: supportUncertain, FileType: "public.png", Reason: "could not determine the file's type"})
	if !strings.HasPrefix(uncertain, "⚠️") {
		t.Errorf("uncertain preview must lead with a warning: %q", uncertain)
	}
	if !strings.Contains(uncertain, "could not determine the file's type") {
		t.Errorf("uncertain preview should carry the reason: %q", uncertain)
	}
}

func focusCapability(t *testing.T) registry.Capability {
	return lookupCapability(t, "focus_application")
}
func quitCapability(t *testing.T) registry.Capability { return lookupCapability(t, "quit_application") }
func openCapability(t *testing.T) registry.Capability { return lookupCapability(t, "open_application") }
