// mutate_system_test.go tests open_settings' pane→URL mapping and command
// construction. No plan is executed, so no Settings window is ever opened.
package engine

import (
	"context"
	"strings"
	"testing"
)

func TestStageOpenSettings_BuildsOpenURL(t *testing.T) {
	plan, err := stageOpenSettings(context.Background(), lookupCapability(t, "open_settings"), map[string]any{"pane": "printers"})
	if err != nil {
		t.Fatalf("stageOpenSettings: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("open_settings has no undo: Inverse should be nil")
	}
	if plan.Forward.Binary != "open" || len(plan.Forward.Args) != 1 {
		t.Fatalf("Forward = %s %v, want a single-arg open command", plan.Forward.Binary, plan.Forward.Args)
	}
	if !strings.HasPrefix(plan.Forward.Args[0], "x-apple.systempreferences:") {
		t.Errorf("expected a System Settings URL, got %q", plan.Forward.Args[0])
	}
}

// TestStageOpenSettings_EveryPaneHasURL confirms every pane the manifest enum
// admits resolves to a URL, so the registry/engine drift guard in the server
// package is backed by an engine-side completeness check too.
func TestStageOpenSettings_EveryPaneHasURL(t *testing.T) {
	for _, pane := range SettingsPaneKeys() {
		plan, err := stageOpenSettings(context.Background(), lookupCapability(t, "open_settings"), map[string]any{"pane": pane})
		if err != nil {
			t.Errorf("pane %q: %v", pane, err)
			continue
		}
		if plan.Forward.Args[0] == "" {
			t.Errorf("pane %q produced an empty URL", pane)
		}
	}
}

// TestStageOpenSettings_HandoffPaneURLs pins the exact deep-link URLs of the
// guided hand-off panes added for no-CLI actions (Focus toggles, keyboard
// languages, iCloud sign-in). The generic tests above only check non-emptiness,
// which would not catch a mistyped identifier — and apple_id's identifier is
// easy to get wrong (it keeps the legacy "com.apple.systempreferences." prefix
// rather than the "-Settings.extension" shape of its neighbors).
func TestStageOpenSettings_HandoffPaneURLs(t *testing.T) {
	want := map[string]string{
		"focus":    "x-apple.systempreferences:com.apple.Focus-Settings.extension",
		"keyboard": "x-apple.systempreferences:com.apple.Keyboard-Settings.extension",
		"apple_id": "x-apple.systempreferences:com.apple.systempreferences.AppleIDSettings",
	}
	for pane, url := range want {
		plan, err := stageOpenSettings(context.Background(), lookupCapability(t, "open_settings"), map[string]any{"pane": pane})
		if err != nil {
			t.Errorf("pane %q: %v", pane, err)
			continue
		}
		if got := plan.Forward.Args[0]; got != url {
			t.Errorf("pane %q URL = %q, want %q", pane, got, url)
		}
	}
}

func TestStageOpenSettings_RejectsUnknownPane(t *testing.T) {
	if _, err := stageOpenSettings(context.Background(), lookupCapability(t, "open_settings"), map[string]any{"pane": "nonsense"}); err == nil {
		t.Error("an unknown pane should be rejected")
	}
}

// --- notify ---------------------------------------------------------------

// TestStageNotify_BuildsOsascriptCommand checks the forward command layout: a
// fixed osascript script with message and title bound as data AFTER the "--"
// terminator, an irreversible plan (nil Inverse), and a preview that says so.
func TestStageNotify_BuildsOsascriptCommand(t *testing.T) {
	plan, err := stageNotify(context.Background(), lookupCapability(t, "notify"), map[string]any{
		"message": "Export finished",
		"title":   "Backup",
	})
	if err != nil {
		t.Fatalf("stageNotify: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("notify is irreversible: Inverse should be nil")
	}
	// The preview states the irreversibility REASON (a shown notification cannot be
	// recalled) but must NOT repeat the phrase "cannot be undone": the auto-commit
	// path appends that suffix itself, so duplicating it here would read redundantly.
	if !strings.Contains(plan.Preview, "cannot be recalled") {
		t.Errorf("preview should state why the action is irreversible, got %q", plan.Preview)
	}
	if strings.Contains(plan.Preview, "cannot be undone") {
		t.Errorf("preview must not repeat the server's 'cannot be undone' suffix, got %q", plan.Preview)
	}
	if plan.Forward.Binary != "osascript" {
		t.Fatalf("forward binary = %q, want osascript", plan.Forward.Binary)
	}
	// Argv must be: -e <script> -- <message> <title>. The "--" terminator makes
	// message/title data, not osascript options.
	want := []string{"-e", notifyAppleScript, "--", "Export finished", "Backup"}
	if len(plan.Forward.Args) != len(want) {
		t.Fatalf("forward argv = %q, want %q", plan.Forward.Args, want)
	}
	for i := range want {
		if plan.Forward.Args[i] != want[i] {
			t.Errorf("forward argv[%d] = %q, want %q", i, plan.Forward.Args[i], want[i])
		}
	}
}

// TestStageNotify_FlagLikeValuesLandAsData is the required injection regression:
// a title (or message) of "-e" — osascript's execute-script flag — must reach
// the script as data after "--", never be parsed as an option.
func TestStageNotify_FlagLikeValuesLandAsData(t *testing.T) {
	plan, err := stageNotify(context.Background(), lookupCapability(t, "notify"), map[string]any{
		"message": "-e",
		"title":   "-rf",
	})
	if err != nil {
		t.Fatalf("stageNotify: %v", err)
	}
	term := indexOf(plan.Forward.Args, "--")
	if term < 0 {
		t.Fatalf("no '--' terminator in argv %q", plan.Forward.Args)
	}
	if !appearsAfter(plan.Forward.Args, "-e", term) {
		t.Errorf("message %q did not land after the terminator in argv %q", "-e", plan.Forward.Args)
	}
	if !appearsAfter(plan.Forward.Args, "-rf", term) {
		t.Errorf("title %q did not land after the terminator in argv %q", "-rf", plan.Forward.Args)
	}
}

// TestStageNotify_DefaultTitleApplied confirms that when title is omitted, the
// full Stage path (which runs normalizeParams) substitutes the manifest default,
// so the notification always has a title line.
func TestStageNotify_DefaultTitleApplied(t *testing.T) {
	c := lookupCapability(t, "notify")
	normalized, err := normalizeParams(c, map[string]any{"message": "hi"})
	if err != nil {
		t.Fatalf("normalizeParams: %v", err)
	}
	plan, err := stageNotify(context.Background(), c, normalized)
	if err != nil {
		t.Fatalf("stageNotify: %v", err)
	}
	// argv tail is <message> <title>; the last element is the (defaulted) title.
	got := plan.Forward.Args[len(plan.Forward.Args)-1]
	if got != "Notification" {
		t.Errorf("default title = %q, want %q", got, "Notification")
	}
}

func TestStageNotify_Rejections(t *testing.T) {
	c := lookupCapability(t, "notify")
	cases := map[string]map[string]any{
		"empty message":   {"message": "", "title": "x"},
		"oversized body":  {"message": strings.Repeat("a", maxNotifyMessageBytes+1), "title": "x"},
		"oversized title": {"message": "hi", "title": strings.Repeat("t", maxNotifyTitleBytes+1)},
	}
	for name, in := range cases {
		if _, err := stageNotify(context.Background(), c, in); err == nil {
			t.Errorf("%s: expected rejection, got nil error", name)
		}
	}
}

// --- speak ----------------------------------------------------------------

// TestStageSpeak_BuildsSayCommand checks the forward command layout: `say` with
// the text as a single positional operand AFTER "--", an irreversible plan, and
// a preview that says so.
func TestStageSpeak_BuildsSayCommand(t *testing.T) {
	plan, err := stageSpeak(context.Background(), lookupCapability(t, "speak"), map[string]any{"text": "hello there"})
	if err != nil {
		t.Fatalf("stageSpeak: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("speak is irreversible: Inverse should be nil")
	}
	// The preview keeps the helpful detail (audio plays immediately) but must NOT
	// repeat "cannot be undone" — the auto-commit path appends that suffix itself.
	if !strings.Contains(plan.Preview, "plays immediately") {
		t.Errorf("preview should note that audio plays immediately, got %q", plan.Preview)
	}
	if strings.Contains(plan.Preview, "cannot be undone") {
		t.Errorf("preview must not repeat the server's 'cannot be undone' suffix, got %q", plan.Preview)
	}
	want := []string{"--", "hello there"}
	if plan.Forward.Binary != "say" || len(plan.Forward.Args) != len(want) {
		t.Fatalf("forward = %s %q, want say %q", plan.Forward.Binary, plan.Forward.Args, want)
	}
	for i := range want {
		if plan.Forward.Args[i] != want[i] {
			t.Errorf("forward argv[%d] = %q, want %q", i, plan.Forward.Args[i], want[i])
		}
	}
}

// TestStageSpeak_FlagLikeTextLandsAsData is the required injection regression:
// a text value of "-v Alex" (which `say` would otherwise parse as its voice
// flag) must be the operand immediately after "--", i.e. inert data.
func TestStageSpeak_FlagLikeTextLandsAsData(t *testing.T) {
	for _, hostile := range []string{"-v", "-r", "--file-format", "-o /tmp/x"} {
		plan, err := stageSpeak(context.Background(), lookupCapability(t, "speak"), map[string]any{"text": hostile})
		if err != nil {
			t.Errorf("%q: stageSpeak: %v", hostile, err)
			continue
		}
		// The "--" must sit immediately before the text so `say` reads it as data.
		if len(plan.Forward.Args) != 2 || plan.Forward.Args[0] != "--" || plan.Forward.Args[1] != hostile {
			t.Errorf("%q: argv = %q, want [\"--\" %q]", hostile, plan.Forward.Args, hostile)
		}
	}
}

func TestStageSpeak_Rejections(t *testing.T) {
	c := lookupCapability(t, "speak")
	cases := map[string]map[string]any{
		"empty text":     {"text": ""},
		"oversized text": {"text": strings.Repeat("a", maxSpeakChars+1)},
	}
	for name, in := range cases {
		if _, err := stageSpeak(context.Background(), c, in); err == nil {
			t.Errorf("%s: expected rejection, got nil error", name)
		}
	}
}

// TestStageSpeak_MultibyteCharCap confirms the cap counts characters, not bytes:
// exactly maxSpeakChars multibyte runes is accepted even though its byte length
// far exceeds the limit.
func TestStageSpeak_MultibyteCharCap(t *testing.T) {
	text := strings.Repeat("é", maxSpeakChars) // 2 bytes each => well over maxSpeakChars bytes
	if _, err := stageSpeak(context.Background(), lookupCapability(t, "speak"), map[string]any{"text": text}); err != nil {
		t.Errorf("exactly maxSpeakChars runes should be accepted, got %v", err)
	}
}
