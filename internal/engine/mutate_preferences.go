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
