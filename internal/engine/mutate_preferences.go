// mutate_preferences.go implements write_setting: a curated, allowlisted
// wrapper around macOS's `defaults` CLI (the tool for reading/writing the
// property-list preferences apps and the system use).
//
// # Why this is curated rather than a generic `defaults write`
//
// `defaults write <domain> <key> <value>` is, on its own, unrestricted within
// the calling user's account — and some settings it can reach are genuinely
// dangerous (e.g. disabling the password prompt after sleep). Exposing raw
// domain/key parameters would mean the model could target any preference, most
// of which neither it nor the user reviewing a confirmation prompt can assess
// the consequences of. So this mutator does NOT take a domain/key — it takes a
// closed `setting` enum (validated twice: once by the registry's TypeEnum
// check against the manifest, once again here in defaultsAllowlist as defense
// in depth against the two ever drifting apart) naming one of a curated list of
// known-safe, reversible, non-security-relevant boolean toggles. The actual
// domain/key pair behind each setting lives in this Go map, never in
// model-controlled input — the same posture policy.allowedBinDirs takes for
// trusted binaries.
//
// # Why undo needs a probe, unlike mkdir
//
// mkdir's inverse (rmdir) needs no information about prior state. A preference
// toggle's inverse does: undo must restore whatever value was there before, not
// some hardcoded default. So staging here reads the current value first (a
// harmless `defaults read`) and bakes that exact prior value into the inverse
// command — the same "resolve everything at stage time" discipline as mkdir,
// just with a state-capture step mkdir didn't need.
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// defaultsSetting is one curated, allowlisted entry: the real domain/key behind
// a setting name, plus a human-readable label for previews.
type defaultsSetting struct {
	domain string
	key    string
	label  string
}

// defaultsAllowlist is the closed set of preferences write_setting may touch.
// Every entry here is a boolean toggle that is reversible, well-documented, and
// has no security or login implications. This list — and ONLY this list — must
// also appear verbatim as the "setting" enum in
// internal/registry/manifests/preferences.json; TestDefaultsAllowlist_MatchesManifestEnum
// in internal/server guards against the two drifting apart.
//
// To add a new curated setting: add an entry here AND to the manifest's enum.
// Keeping the domain/key mapping in Go rather than JSON is deliberate — it
// means adding a setting requires a reviewed code change, not just a data edit,
// which is the right amount of friction for something security-adjacent.
var defaultsAllowlist = map[string]defaultsSetting{
	"finder_show_hidden_files":        {"com.apple.finder", "AppleShowAllFiles", "Finder: show hidden files"},
	"finder_show_all_extensions":      {"NSGlobalDomain", "AppleShowAllExtensions", "Finder: always show file extensions"},
	"finder_show_path_bar":            {"com.apple.finder", "ShowPathbar", "Finder: show the path bar"},
	"finder_show_status_bar":          {"com.apple.finder", "ShowStatusBar", "Finder: show the status bar"},
	"finder_warn_on_extension_change": {"com.apple.finder", "FXEnableExtensionChangeWarning", "Finder: warn when changing a file extension"},
	"dock_autohide":                   {"com.apple.dock", "autohide", "Dock: auto-hide"},
	"dock_show_recents":               {"com.apple.dock", "show-recents", "Dock: show recently used applications"},
	"dock_minimize_to_app_icon":       {"com.apple.dock", "minimize-to-application", "Dock: minimize windows into their application's icon"},
	"dock_show_process_indicators":    {"com.apple.dock", "show-process-indicators", "Dock: show indicator lights for open applications"},
	"screenshot_disable_shadow":       {"com.apple.screencapture", "disable-shadow", "Screenshots: disable the drop shadow around window screenshots"},
	"global_press_and_hold_accents":   {"NSGlobalDomain", "ApplePressAndHoldEnabled", "Keyboard: enable press-and-hold for accented characters (when off, holding a key repeats it instead)"},
	"global_autocorrect":              {"NSGlobalDomain", "NSAutomaticSpellingCorrectionEnabled", "Global: enable automatic spelling correction"},
	"global_smart_quotes":             {"NSGlobalDomain", "NSAutomaticQuoteSubstitutionEnabled", "Global: enable smart quote substitution"},
	"global_smart_dashes":             {"NSGlobalDomain", "NSAutomaticDashSubstitutionEnabled", "Global: enable smart dash substitution"},
	"global_period_substitution":      {"NSGlobalDomain", "NSAutomaticPeriodSubstitutionEnabled", "Global: enable double-space-to-period substitution"},

	"accessibility_reduce_motion":               {"com.apple.universalaccess", "reduceMotion", "Accessibility: reduce motion (minimize animations)"},
	"accessibility_reduce_transparency":         {"com.apple.universalaccess", "reduceTransparency", "Accessibility: reduce transparency"},
	"accessibility_increase_contrast":           {"com.apple.universalaccess", "increaseContrast", "Accessibility: increase contrast"},
	"accessibility_differentiate_without_color": {"com.apple.universalaccess", "differentiateWithoutColor", "Accessibility: differentiate without color"},
}

// DefaultsAllowlistKeys returns the sorted setting names defaultsAllowlist
// recognizes. It exists solely so a cross-package test (internal/server, which
// depends on both this package and the registry) can assert this list exactly
// matches the manifest's "setting" enum, catching drift between the two
// declarations of the same allowlist.
func DefaultsAllowlistKeys() []string {
	keys := make([]string, 0, len(defaultsAllowlist))
	for k := range defaultsAllowlist {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// stageWriteSetting stages a curated preference toggle.
//
// It looks up the requested setting in defaultsAllowlist (rejecting anything
// not on the list, even though the registry's enum validation should already
// have caught it — a mutator must never trust that a future manifest edit kept
// the two lists in sync), reads the setting's current value, and refuses to
// proceed if that value is not a plain boolean ("1"/"0"/unset) — staging must
// never guess at how to round-trip a value shape it does not understand.
func stageWriteSetting(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	setting, _ := getString(in, "setting")
	value := getBool(in, "value")

	s, ok := defaultsAllowlist[setting]
	if !ok {
		return nil, fmt.Errorf("write_setting: %q is not a recognized curated setting", setting)
	}

	prior, unset, err := probeDefaultsValue(ctx, s.domain, s.key)
	if err != nil {
		return nil, fmt.Errorf("write_setting: could not read the current value of %s: %w", s.label, err)
	}

	var priorDesc, undoDesc string
	var inverse Command
	switch {
	case unset:
		priorDesc = "currently unset"
		undoDesc = "remove this key, restoring its unset state"
		inverse = Command{Binary: "defaults", Args: []string{"delete", s.domain, s.key}}
	case prior == "1" || prior == "0":
		priorBool := prior == "1"
		priorDesc = fmt.Sprintf("currently %v", priorBool)
		undoDesc = fmt.Sprintf("restore it to %v", priorBool)
		inverse = Command{Binary: "defaults", Args: []string{"write", s.domain, s.key, "-bool", boolArg(priorBool)}}
	default:
		return nil, fmt.Errorf("write_setting: current value of %s is %q, not a plain boolean; refusing to stage to avoid corrupting it", s.label, prior)
	}

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"%s: set to %v (%s). Undo will %s. Note: some apps only pick up preference changes after they restart; this server does not restart any app automatically.",
			s.label, value, priorDesc, undoDesc,
		),
		Forward: Command{Binary: "defaults", Args: []string{"write", s.domain, s.key, "-bool", boolArg(value)}},
		Inverse: &inverse,
	}, nil
}

// probeDefaultsValue runs a read-only `defaults read <domain> <key>` and
// reports the raw value, or unset=true if the key does not exist. This is the
// state-capture step a static inverse like mkdir's never needed: undo cannot
// know what to restore without first asking what was there.
//
// A non-zero exit is only treated as "unset" when defaults' own stderr says
// the domain/key pair does not exist. Any other failure (a malformed domain,
// a permissions problem, or anything else defaults might reject) is returned
// as an error instead — staging must fail loudly there, not silently
// misclassify a real problem as "nothing to restore" and stage an inverse
// that would be wrong.
func probeDefaultsValue(ctx context.Context, domain, key string) (value string, unset bool, err error) {
	bin, err := policy.ResolveBinary("defaults")
	if err != nil {
		return "", false, err
	}
	res, err := runCommand(ctx, bin, "read", domain, key)
	if err != nil {
		return "", false, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(res.Stderr, "does not exist") {
			return "", true, nil
		}
		return "", false, fmt.Errorf("defaults read %s %s: exit %d: %s", domain, key, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), false, nil
}

// boolArg renders a bool as the literal token `defaults write -bool` expects.
func boolArg(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// --- set_appearance: Dark/Light mode toggle via System Events ---------------
//
// set_appearance is the FIRST capability to drive System Events (macOS's
// GUI-scripting bridge) as a MUTATION, which makes it the first to require the
// "System Events" Automation grant on the mutating path. Switching Dark/Light
// mode has no first-party command line (`defaults write -g AppleInterfaceStyle`
// does NOT reliably repaint the running session), so System Events'
// `appearance preferences` is the sanctioned route.
//
// It is reversible/low and runs in the auto-commit lane: flipping appearance is
// a benign, instantly-reversible cosmetic change, so forcing a stage→execute
// round-trip would be pure friction — but an undo token is still returned so the
// prior appearance can be restored in one step.

// readDarkModeScript returns "true" or "false" for the current Dark-mode state.
// It is a FIXED, reviewed script (no interpolation) run read-only at stage time
// to capture the prior state the inverse must restore. Reading via System Events
// (rather than `defaults read -g AppleInterfaceStyle`) means the SAME Automation
// permission the forward needs is exercised at stage time, so a missing grant
// surfaces as one clear, mapped error before anything is committed — consistent
// with how the Calendar/Reminders mutators probe prior state through osascript.
const readDarkModeScript = `on run argv
	tell application "System Events"
		tell appearance preferences
			return dark mode as text
		end tell
	end tell
end run`

// setDarkModeScript sets Dark mode on/off. The desired state arrives as data
// bound to `on run argv` (item 1 = "true"/"false"); osascriptCommand inserts the
// "--" terminator ahead of it so the value can never be parsed as an osascript
// option. The script itself is a fixed constant — model input only ever picks
// the closed "dark"/"light" enum, which this file maps to the boolean token.
const setDarkModeScript = `on run argv
	tell application "System Events"
		tell appearance preferences
			set dark mode to ((item 1 of argv) is "true")
		end tell
	end tell
end run`

// appearanceLabel renders a dark-mode boolean as the user-facing mode name.
func appearanceLabel(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}

// stageSetAppearance stages (for immediate auto-commit) a Dark/Light appearance
// switch. It probes the current appearance via System Events, bakes that prior
// state into the inverse, and returns a plan whose forward sets the requested
// mode and whose inverse restores the one observed at stage time.
func stageSetAppearance(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	mode, _ := getString(in, "mode")

	var wantDark bool
	switch mode {
	case "dark":
		wantDark = true
	case "light":
		wantDark = false
	default:
		// Unreachable when staged through the registry (the enum is validated),
		// but defended because this also runs in direct unit tests.
		return nil, fmt.Errorf("set_appearance: unknown mode %q (want \"dark\" or \"light\")", mode)
	}

	priorDark, err := probeDarkMode(ctx)
	if err != nil {
		return nil, err
	}
	return planSetAppearance(wantDark, priorDark), nil
}

// planSetAppearance is the pure half of stageSetAppearance: given the desired and
// prior Dark-mode states, it assembles the forward/inverse commands and preview
// with no side effects. Split out so the argv layout, "--" terminator, and
// preview text can be unit-tested without a live (permission-gated) System Events
// probe.
func planSetAppearance(wantDark, priorDark bool) *StagedPlan {
	inverse := osascriptCommand(setDarkModeScript, boolArg(priorDark))
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Switch the system appearance to %s mode (currently %s mode). Undo will switch it back to %s mode.",
			appearanceLabel(wantDark), appearanceLabel(priorDark), appearanceLabel(priorDark),
		),
		Forward: osascriptCommand(setDarkModeScript, boolArg(wantDark)),
		Inverse: &inverse,
	}
}

// probeDarkMode reads the current Dark-mode state via System Events. A non-zero
// osascript exit — most commonly a denied Automation grant on first use — is
// mapped to an actionable message rather than a bare AppleScript error.
func probeDarkMode(ctx context.Context) (bool, error) {
	res, err := runOsascript(ctx, readDarkModeScript)
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 {
		return false, systemEventsScriptError("set_appearance", res.Stderr)
	}
	return strings.TrimSpace(res.Stdout) == "true", nil
}

// systemEventsScriptError surfaces the most likely cause of a failed System
// Events script — Automation access to System Events not yet granted — with an
// actionable hint pointing at the exact Settings pane, mirroring appScriptError
// (builtins_apps.go). It is shared so every System-Events-backed capability
// (set_appearance now; window management later) maps this failure identically.
func systemEventsScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: controlling System Events failed (%s). If this is the first use, macOS may need you to grant this app access to System Events in System Settings → Privacy & Security → Automation", op, stderr)
}
