// mutate_system.go implements the `system` domain's mutating capabilities:
// open_settings (the guided System-Settings hand-off) and the two agent→human
// back-channels notify (a Notification Center banner) and speak (text-to-speech).
//
// # open_settings — why it exists
//
// Several system toggles a "command center" would want — connecting/re-enabling
// a printer, joining a Wi-Fi network, turning Bluetooth on/off, changing battery
// or Low-Power settings — require administrator rights (lpadmin/cupsenable,
// privileged power management, etc.) that cannot be obtained over this server's
// non-interactive transport. Others — pairing a Bluetooth device, signing into
// an Apple Account/iCloud, toggling a Focus, adding a keyboard language,
// starting screen mirroring — have NO first-party command line at all, admin or
// not. Rather than fail either way, open_settings deep-links the user straight
// to the relevant pane so they can finish with a click; the tool description
// tells the model to include the exact click-path in its reply.
//
// # auto-commit, and why the URL is built in Go
//
// Opening a window is a benign, immediate action, so this is AUTO-COMMIT (it runs
// without the execute gate) and irreversible (there is nothing to undo). The model
// never supplies a URL: it picks a pane from a closed enum, and this file maps
// that enum to a vetted "x-apple.systempreferences:" identifier — the same
// data-not-code posture write_setting takes for defaults domains/keys.
//
// # macOS version
//
// The project's minimum supported OS is macOS 13 Ventura, which re-architected
// System Settings, so only the modern (Ventura+) pane identifiers are used — no
// per-version selection. If a given identifier is not recognised on some future
// release, `open` still launches System Settings (to its default pane), so the
// action degrades gracefully rather than erroring.
package engine

import (
	"context"
	"fmt"
	"sort"
	"unicode/utf8"

	"mcp-server-mac-os/internal/registry"
)

// settingsPaneURLs maps each open_settings "pane" enum value to its Ventura+
// System Settings deep-link URL. This map — not model input — is the only source
// of URLs, and it must stay in sync with the "pane" enum in
// internal/registry/manifests/system.json.
var settingsPaneURLs = map[string]string{
	"printers":      "x-apple.systempreferences:com.apple.Print-Scan-Settings.extension",
	"wifi":          "x-apple.systempreferences:com.apple.wifi-settings-extension",
	"bluetooth":     "x-apple.systempreferences:com.apple.BluetoothSettings",
	"battery":       "x-apple.systempreferences:com.apple.Battery-Settings.extension",
	"accessibility": "x-apple.systempreferences:com.apple.Accessibility-Settings.extension",
	"displays":      "x-apple.systempreferences:com.apple.Displays-Settings.extension",
	"sound":         "x-apple.systempreferences:com.apple.Sound-Settings.extension",
	"network":       "x-apple.systempreferences:com.apple.Network-Settings.extension",
	// Hand-off panes for actions with no first-party CLI at all (not merely
	// admin-gated): Focus/Do Not Disturb toggles, keyboard input-source
	// management, and Apple Account/iCloud sign-in. Note apple_id uses the
	// legacy "com.apple.systempreferences." prefix — that IS its Ventura+
	// identifier, unlike the "-Settings.extension" panes around it.
	"focus":    "x-apple.systempreferences:com.apple.Focus-Settings.extension",
	"keyboard": "x-apple.systempreferences:com.apple.Keyboard-Settings.extension",
	"apple_id": "x-apple.systempreferences:com.apple.systempreferences.AppleIDSettings",
}

// stageOpenSettings stages (for immediate auto-commit) opening a System Settings
// pane. The forward command is `open <url>`; there is no inverse.
func stageOpenSettings(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	pane, _ := getString(in, "pane")
	url, ok := settingsPaneURLs[pane]
	if !ok {
		// Unreachable when staged through the registry (the enum is validated),
		// but defended because this also runs in direct unit tests.
		return nil, fmt.Errorf("open_settings: unknown pane %q", pane)
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Open the %q pane of System Settings.", pane),
		Forward: Command{Binary: "open", Args: []string{url}},
		Inverse: nil,
	}, nil
}

// SettingsPaneKeys returns the sorted pane names open_settings recognises. It
// exists so a cross-package test can assert this map matches the manifest's
// "pane" enum, catching drift between the two declarations.
func SettingsPaneKeys() []string {
	keys := make([]string, 0, len(settingsPaneURLs))
	for k := range settingsPaneURLs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The agent→human back-channels (notify, speak) exist so a long-running or
// background task can get the user's attention — "the export finished", "I need
// input" — without them watching the transcript. Both are one-shot side effects
// with nothing to undo (a banner has already been posted; audio has already
// played), so both are irreversible and run in the auto-commit lane: forcing a
// stage→execute round-trip on a harmless "ping the user" would be pure friction.
// The auto-commit lane still renders "this cannot be undone" from the nil Inverse.
const (
	// maxNotifyMessageBytes / maxNotifyTitleBytes bound the two notification
	// strings. Notification Center itself truncates long banners, so these caps
	// are about keeping the staged plan and its preview small, not fidelity.
	maxNotifyMessageBytes = 1000
	maxNotifyTitleBytes   = 200
	// maxSpeakChars bounds spoken text. `say` runs under the engine's 2-minute
	// subprocess kill; ~2000 characters is well under a minute of speech at the
	// default rate, so normal use never approaches the deadline. If the request is
	// cancelled (client drops the socket), the context binding stops playback.
	maxSpeakChars = 2000
)

// notifyAppleScript posts a Notification Center banner. Argv layout (fixed, not
// model-controlled): item 1 = message body, item 2 = title. Both arrive as data
// bound to `on run argv`; osascriptCommand inserts the "--" terminator ahead of
// them, so a value like a title of "-e" reaches the script as text, never as an
// osascript option (the option-injection guard in applescript.go / CLAUDE.md §4).
const notifyAppleScript = `on run argv
	display notification (item 1 of argv) with title (item 2 of argv)
end run`

// stageNotify stages (for immediate auto-commit) a Notification Center banner.
// There is no inverse — a posted notification cannot be recalled — so the plan
// carries a nil Inverse and the preview says the action cannot be undone.
//
// No Automation (Apple Events) grant is needed to post a notification; whether
// the banner is actually shown is governed by the HOST MCP CLIENT's own
// Notifications permission (System Settings → Notifications), which the tool
// description points the user at if nothing appears.
func stageNotify(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	message, _ := getString(in, "message")
	title, _ := getString(in, "title")

	if message == "" {
		return nil, fmt.Errorf("notify: 'message' is required")
	}
	if len(message) > maxNotifyMessageBytes {
		return nil, fmt.Errorf("notify: message is %d bytes, exceeding the %d-byte limit", len(message), maxNotifyMessageBytes)
	}
	if len(title) > maxNotifyTitleBytes {
		return nil, fmt.Errorf("notify: title is %d bytes, exceeding the %d-byte limit", len(title), maxNotifyTitleBytes)
	}

	return &StagedPlan{
		// The reason (a shown notification cannot be recalled) enriches the preview,
		// but we deliberately do NOT repeat the phrase "cannot be undone": the
		// auto-commit path appends its own "Done: <name>. This cannot be undone."
		// suffix for a nil-inverse op (server.autoCommitMutation), so including it
		// here too would read as duplicated output. open_settings' preview omits it
		// for the same reason.
		Preview: fmt.Sprintf("Post a macOS notification titled %q: %q. A shown notification cannot be recalled.", title, message),
		// Argv order mirrors the script's item indices: message first, title second.
		Forward: osascriptCommand(notifyAppleScript, message, title),
		Inverse: nil, // irreversible: a shown notification cannot be recalled
	}, nil
}

// stageSpeak stages (for immediate auto-commit) speaking text aloud via `say`.
// There is no inverse — audio, once played, cannot be unheard — so the plan
// carries a nil Inverse and the preview says the action cannot be undone.
//
// Injection: the text is passed as a single positional operand AFTER a "--"
// end-of-options terminator, so a value beginning with "-" (e.g. "-v Alex", which
// `say` would otherwise read as its voice flag) is treated strictly as text to
// speak. This mirrors the osascript "--" discipline; `say` honours "--" (verified
// in mutate_system_test.go's dash-leading regression case).
func stageSpeak(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	text, _ := getString(in, "text")

	if text == "" {
		return nil, fmt.Errorf("speak: 'text' is required")
	}
	// Count characters (not bytes) so the cap matches the human notion of "2000
	// characters"; the point is bounding speech duration under the 2-minute kill.
	if n := utf8.RuneCountInString(text); n > maxSpeakChars {
		return nil, fmt.Errorf("speak: text is %d characters, exceeding the %d-character limit", n, maxSpeakChars)
	}

	return &StagedPlan{
		// As with notify, keep the reason (audio plays at once) but not the phrase
		// "cannot be undone" — the auto-commit path appends that suffix itself for a
		// nil-inverse op, so repeating it here would duplicate the message.
		Preview: fmt.Sprintf("Speak this text aloud through the Mac's speakers: %q. Audio plays immediately.", text),
		// "--" makes the (possibly dash-leading) text a pure positional operand.
		Forward: Command{Binary: "say", Args: []string{"--", text}},
		Inverse: nil, // irreversible: audio, once played, cannot be unheard
	}, nil
}
