// builtins_preferences_test.go exercises read_setting against a synthetic,
// disposable defaults domain — never a real curated domain (com.apple.finder,
// com.apple.dock, ...), so a test run never inspects or mutates the developer's
// or CI machine's actual settings. It reuses withSyntheticSetting from
// mutate_preferences_test.go (same package).
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// readSettingCap mirrors the shape the registry hands runReadSetting: a single
// "setting" name. TypeString (not TypeEnum) so the builtin's own allowlist
// lookup is exercised against a synthetic name, independent of the manifest's
// enum restriction (cross-checked in internal/server).
var readSettingCap = registry.Capability{
	Name:          "read_setting",
	Category:      "preferences",
	Binary:        "defaults",
	Reversibility: registry.ReadOnly,
	Risk:          registry.RiskNone,
	Builder:       "read_setting",
	Params: []registry.ParamSpec{
		{Name: "setting", Type: registry.TypeString, Required: true, Arg: registry.ArgRule{Kind: registry.ArgNone}},
	},
}

// TestReadSetting_Unset confirms a key that does not exist reports "unset".
func TestReadSetting_Unset(t *testing.T) {
	setting := withSyntheticSetting(t)
	out, err := runReadSetting(context.Background(), readSettingCap, map[string]any{"setting": setting})
	if err != nil {
		t.Fatalf("runReadSetting: %v", err)
	}
	if !strings.Contains(out, "unset") {
		t.Errorf("expected an unset report, got %q", out)
	}
}

// TestReadSetting_OnAndOff confirms a seeded boolean value renders as on/off.
func TestReadSetting_OnAndOff(t *testing.T) {
	setting := withSyntheticSetting(t)
	eng := New()
	ctx := context.Background()
	s := defaultsAllowlist[setting]

	for _, tc := range []struct {
		seed string
		want string
	}{
		{"true", "on"},
		{"false", "off"},
	} {
		seed := Command{Binary: "defaults", Args: []string{"write", s.domain, s.key, "-bool", tc.seed}}
		if _, err := eng.RunCommand(ctx, seed); err != nil {
			t.Fatalf("seeding %s: %v", tc.seed, err)
		}
		out, err := runReadSetting(ctx, readSettingCap, map[string]any{"setting": setting})
		if err != nil {
			t.Fatalf("runReadSetting: %v", err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("value %s: expected %q in output, got %q", tc.seed, tc.want, out)
		}
	}
}

// TestReadSetting_RejectsUnknownSetting confirms a setting name absent from
// defaultsAllowlist is refused — the builtin must not trust that registry-level
// enum validation already caught it (a future manifest edit could drift).
func TestReadSetting_RejectsUnknownSetting(t *testing.T) {
	if _, err := runReadSetting(context.Background(), readSettingCap, map[string]any{"setting": "not_a_real_setting"}); err == nil {
		t.Fatal("expected runReadSetting to reject an unrecognized setting name")
	}
}
