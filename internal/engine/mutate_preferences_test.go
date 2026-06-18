// mutate_preferences_test.go exercises write_setting's staging logic and a real
// end-to-end forward/inverse round trip.
//
// SAFETY: tests must never run `defaults write`/`delete` against the real
// curated domains (com.apple.finder, com.apple.dock, NSGlobalDomain, ...) —
// doing so would mutate the developer's or CI machine's actual settings. Every
// test that exercises a real `defaults` subprocess does so against a synthetic
// allowlist entry temporarily inserted into defaultsAllowlist, pointing at a
// disposable plist file under t.TempDir() (confirmed working: `defaults write
// <path> <key> -bool true` treats an absolute path as a one-off domain). The
// synthetic entry is removed via t.Cleanup so it can never leak into another
// test or the production list.
package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// withSyntheticSetting temporarily registers a defaultsAllowlist entry pointing
// at a throwaway plist file under t.TempDir() and returns its setting name. The
// entry is removed automatically when the test ends.
func withSyntheticSetting(t *testing.T) (settingName string) {
	t.Helper()
	domain := filepath.Join(t.TempDir(), "synthetic")
	const name = "test_only_synthetic_setting"
	defaultsAllowlist[name] = defaultsSetting{domain: domain, key: "TestKey", label: "synthetic test setting"}
	t.Cleanup(func() { delete(defaultsAllowlist, name) })
	return name
}

var writeSettingCap = registry.Capability{
	Name:          "write_setting",
	Category:      "preferences",
	Binary:        "defaults",
	Reversibility: registry.Reversible,
	Risk:          registry.RiskMedium,
	Builder:       "write_setting",
	Params: []registry.ParamSpec{
		// TypeString here, not TypeEnum: these tests exercise the mutator's own
		// allowlist lookup against a synthetic setting name, independent of the
		// registry-level enum restriction production traffic goes through (that
		// restriction is exercised by the manifest's real enum and cross-checked
		// against this allowlist in internal/server).
		{Name: "setting", Type: registry.TypeString, Required: true, Arg: registry.ArgRule{Kind: registry.ArgNone}},
		{Name: "value", Type: registry.TypeBool, Required: true, Arg: registry.ArgRule{Kind: registry.ArgNone}},
	},
}

// TestStageWriteSetting_UnsetKey confirms staging a key that does not yet exist
// produces a delete inverse and a preview noting it is currently unset.
func TestStageWriteSetting_UnsetKey(t *testing.T) {
	setting := withSyntheticSetting(t)
	plan, err := New().Stage(context.Background(), writeSettingCap, map[string]any{"setting": setting, "value": true})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if !strings.Contains(plan.Preview, "unset") {
		t.Errorf("preview should note the key is unset: %q", plan.Preview)
	}
	if plan.Inverse == nil || plan.Inverse.Binary != "defaults" || plan.Inverse.Args[0] != "delete" {
		t.Errorf("inverse for an unset key should be `defaults delete ...`, got %+v", plan.Inverse)
	}
	if plan.Forward.Args[0] != "write" || plan.Forward.Args[len(plan.Forward.Args)-1] != "true" {
		t.Errorf("forward should write the requested value, got %v", plan.Forward.Args)
	}
}

// TestWriteSetting_RoundTrip drives the full stage -> commit -> undo flow
// against the synthetic disposable domain: commit should write the new boolean,
// and undo should restore the prior (unset) state.
func TestWriteSetting_RoundTrip(t *testing.T) {
	setting := withSyntheticSetting(t)
	eng := New()
	ctx := context.Background()
	s := defaultsAllowlist[setting]

	// Stage and commit: the key starts unset.
	plan, err := eng.Stage(ctx, writeSettingCap, map[string]any{"setting": setting, "value": true})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := eng.RunCommand(ctx, plan.Forward); err != nil {
		t.Fatalf("RunCommand(forward): %v", err)
	}
	got, unset, err := probeDefaultsValue(ctx, s.domain, s.key)
	if err != nil || unset || got != "1" {
		t.Fatalf("after commit, expected the key to read \"1\", got value=%q unset=%v err=%v", got, unset, err)
	}

	// Undo: should remove the key again (it was unset before).
	if _, err := eng.RunCommand(ctx, *plan.Inverse); err != nil {
		t.Fatalf("RunCommand(inverse): %v", err)
	}
	if _, unset, err := probeDefaultsValue(ctx, s.domain, s.key); err != nil || !unset {
		t.Fatalf("after undo, expected the key to be unset again, unset=%v err=%v", unset, err)
	}
}

// TestWriteSetting_RestoresPriorTrueValue confirms that when a key already has
// a value, staging captures it and undo restores that exact value rather than
// just deleting the key.
func TestWriteSetting_RestoresPriorTrueValue(t *testing.T) {
	setting := withSyntheticSetting(t)
	eng := New()
	ctx := context.Background()
	s := defaultsAllowlist[setting]

	// Seed a prior value of true directly, simulating "the setting was already on".
	seed := Command{Binary: "defaults", Args: []string{"write", s.domain, s.key, "-bool", "true"}}
	if _, err := eng.RunCommand(ctx, seed); err != nil {
		t.Fatalf("seeding prior value: %v", err)
	}

	plan, err := eng.Stage(ctx, writeSettingCap, map[string]any{"setting": setting, "value": false})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if !strings.Contains(plan.Preview, "currently true") {
		t.Errorf("preview should report the prior value: %q", plan.Preview)
	}

	if _, err := eng.RunCommand(ctx, plan.Forward); err != nil {
		t.Fatalf("RunCommand(forward): %v", err)
	}
	if got, _, _ := probeDefaultsValue(ctx, s.domain, s.key); got != "0" {
		t.Fatalf("after commit expected \"0\", got %q", got)
	}

	if _, err := eng.RunCommand(ctx, *plan.Inverse); err != nil {
		t.Fatalf("RunCommand(inverse): %v", err)
	}
	if got, unset, _ := probeDefaultsValue(ctx, s.domain, s.key); unset || got != "1" {
		t.Fatalf("after undo expected the key restored to \"1\", got value=%q unset=%v", got, unset)
	}
}

// TestStageWriteSetting_RejectsNonBooleanPrior confirms staging refuses to
// proceed when the existing value is not a plain "1"/"0" boolean, since the
// mutator has no safe way to round-trip an unrecognized value shape.
func TestStageWriteSetting_RejectsNonBooleanPrior(t *testing.T) {
	setting := withSyntheticSetting(t)
	eng := New()
	ctx := context.Background()
	s := defaultsAllowlist[setting]

	seed := Command{Binary: "defaults", Args: []string{"write", s.domain, s.key, "-string", "not-a-bool"}}
	if _, err := eng.RunCommand(ctx, seed); err != nil {
		t.Fatalf("seeding non-boolean prior value: %v", err)
	}

	if _, err := eng.Stage(ctx, writeSettingCap, map[string]any{"setting": setting, "value": true}); err == nil {
		t.Fatal("expected Stage to refuse a non-boolean existing value")
	}
}

// TestStageWriteSetting_RejectsUnknownSetting confirms a setting name absent
// from defaultsAllowlist is refused, even though registry-level enum
// validation should already catch this in production — the mutator itself must
// not trust that invariant blindly.
func TestStageWriteSetting_RejectsUnknownSetting(t *testing.T) {
	if _, err := New().Stage(context.Background(), writeSettingCap, map[string]any{"setting": "not_a_real_setting", "value": true}); err == nil {
		t.Fatal("expected Stage to reject an unrecognized setting name")
	}
}

// TestDefaultsAllowlist_EntriesAreWellFormed is a pure data sanity check — it
// never executes `defaults` against any production entry, only asserts every
// curated setting has the fields a preview/command needs.
func TestDefaultsAllowlist_EntriesAreWellFormed(t *testing.T) {
	for name, s := range defaultsAllowlist {
		if name == "test_only_synthetic_setting" {
			continue // never present outside a test
		}
		if s.domain == "" || s.key == "" || s.label == "" {
			t.Errorf("setting %q has an empty field: %+v", name, s)
		}
	}
}
