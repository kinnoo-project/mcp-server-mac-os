// mutate_system.go implements open_settings: the guided-fallback action that
// opens a specific pane of System Settings for the user.
//
// # Why this exists
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
