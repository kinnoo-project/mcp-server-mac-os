// builtins_preferences.go implements read_setting: the read-only counterpart to
// write_setting (mutate_preferences.go). It answers "what is this setting set to
// right now?" for the same curated, allowlisted preferences write_setting can
// change.
//
// # Why it shares write_setting's allowlist
//
// Exposing a raw `defaults read <domain> <key>` would let the model probe any
// preference in the user's account — most of which neither it nor a reviewer can
// assess. So read_setting takes the SAME closed `setting` enum as write_setting
// and resolves it through the SAME defaultsAllowlist map (mutate_preferences.go):
// the real domain/key pair lives in reviewed Go code, never in model input. Each
// side has its own manifest-drift guard against that shared allowlist
// (internal/server): TestDefaultsAllowlist_MatchesManifestEnum for write_setting's
// enum, TestReadSettingEnum_MatchesDefaultsAllowlist for read_setting's. Together
// they keep the two in lockstep — a setting is either readable AND writable
// through the curated list, or neither.
package engine

import (
	"context"
	"fmt"

	"mcp-server-mac-os/internal/registry"
)

// runReadSetting reports the current value of one curated preference. It looks
// the setting up in defaultsAllowlist (rejecting anything off the list, exactly
// as the mutator does — a builtin must never trust that a future manifest edit
// kept the enum and the allowlist in sync), reads the value with a harmless
// `defaults read`, and renders it in plain language.
//
// Every curated setting is a boolean toggle, so the value is normally "1"/"0" or
// unset; those render as on/off/unset. Any other shape (a setting that was hand-
// edited to a non-boolean) is reported verbatim rather than guessed at — a read
// should surface reality, not sanitize it.
func runReadSetting(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	setting, _ := getString(in, "setting")

	s, ok := defaultsAllowlist[setting]
	if !ok {
		return "", fmt.Errorf("read_setting: %q is not a recognized curated setting", setting)
	}

	value, unset, err := probeDefaultsValue(ctx, s.domain, s.key)
	if err != nil {
		return "", fmt.Errorf("read_setting: could not read the current value of %s: %w", s.label, err)
	}

	switch {
	case unset:
		return fmt.Sprintf("%s: unset (macOS is using its built-in default for this setting).", s.label), nil
	case value == "1":
		return fmt.Sprintf("%s: on (true).", s.label), nil
	case value == "0":
		return fmt.Sprintf("%s: off (false).", s.label), nil
	default:
		return fmt.Sprintf("%s: %s.", s.label, value), nil
	}
}
