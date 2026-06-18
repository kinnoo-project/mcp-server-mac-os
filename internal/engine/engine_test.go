// engine_test.go covers the engine end to end: parameter normalization, generic
// argv assembly (golden vectors), builder validation, and a real `ls` execution
// against a hermetic temp tree.
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

// lsCapability loads the genuine `ls` capability from the embedded registry so
// tests exercise the shipped manifest, not a hand-rolled copy.
func lsCapability(t *testing.T) registry.Capability {
	t.Helper()
	r, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	ls, ok := r.Lookup("ls")
	if !ok {
		t.Fatal("ls capability not found in registry")
	}
	return ls
}

// TestBuildGeneric_LS pins the argv assembled for ls under several inputs,
// including the "--" terminator placement and default substitution.
func TestBuildGeneric_LS(t *testing.T) {
	ls := lsCapability(t)
	cases := []struct {
		name string
		raw  map[string]any
		want []string
	}{
		{"flags and path", map[string]any{"all": true, "long": true, "path": "/tmp"}, []string{"-A", "-l", "--", "/tmp"}},
		{"default path only", map[string]any{}, []string{"--", "."}},
		{"single flag", map[string]any{"recursive": true}, []string{"-R", "--", "."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			norm, err := normalizeParams(ls, tc.raw)
			if err != nil {
				t.Fatalf("normalizeParams: %v", err)
			}
			got, err := buildGeneric(ls, norm)
			if err != nil {
				t.Fatalf("buildGeneric: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNormalizeParams_Errors covers the validation guardrails on a synthetic
// capability that exercises required, unknown, enum, and int rules.
func TestNormalizeParams_Errors(t *testing.T) {
	c := registry.Capability{
		Name: "demo", Summary: "x", Category: "test",
		Reversibility: registry.ReadOnly, Risk: registry.RiskNone,
		Params: []registry.ParamSpec{
			{Name: "must", Type: registry.TypeString, Required: true},
			{Name: "count", Type: registry.TypeInt},
			{Name: "mode", Type: registry.TypeEnum, Enum: []string{"a", "b"}},
		},
	}
	cases := []struct {
		name string
		raw  map[string]any
	}{
		{"missing required", map[string]any{}},
		{"unknown param", map[string]any{"must": "x", "bogus": 1}},
		{"bad enum", map[string]any{"must": "x", "mode": "z"}},
		{"non-integer int", map[string]any{"must": "x", "count": 1.5}},
		{"wrong type", map[string]any{"must": 42}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeParams(c, tc.raw); err == nil {
				t.Fatalf("expected error for %q", tc.name)
			}
		})
	}
}

// TestNormalizeParams_IntFromJSON confirms a JSON number (float64) is accepted
// as an int when whole.
func TestNormalizeParams_IntFromJSON(t *testing.T) {
	c := registry.Capability{
		Name: "demo", Summary: "x", Category: "test",
		Reversibility: registry.ReadOnly, Risk: registry.RiskNone,
		Params: []registry.ParamSpec{{Name: "count", Type: registry.TypeInt}},
	}
	out, err := normalizeParams(c, map[string]any{"count": float64(7)})
	if err != nil {
		t.Fatalf("normalizeParams: %v", err)
	}
	if out["count"] != 7 {
		t.Errorf("count = %v (%T), want int 7", out["count"], out["count"])
	}
}

// TestValidateBuilders flags a capability whose builder is not registered.
func TestValidateBuilders(t *testing.T) {
	e := New()
	good := registry.Capability{Name: "g", Builder: "generic"}
	if err := e.ValidateBuilders([]registry.Capability{good}); err != nil {
		t.Fatalf("generic builder should validate: %v", err)
	}
	bad := registry.Capability{Name: "b", Builder: "does-not-exist"}
	if err := e.ValidateBuilders([]registry.Capability{bad}); err == nil {
		t.Fatal("expected error for unknown builder")
	}
}

// TestValidateBuilders_AcceptsStdin confirms the AcceptsStdin precondition:
// only a read-only, argv-builder-backed capability may set it.
func TestValidateBuilders_AcceptsStdin(t *testing.T) {
	e := New()
	validCap := registry.Capability{Name: "v", Builder: "generic", Reversibility: registry.ReadOnly, AcceptsStdin: true}
	if err := e.ValidateBuilders([]registry.Capability{validCap}); err != nil {
		t.Errorf("a read-only argv-builder capability should be allowed to set accepts_stdin: %v", err)
	}

	mutating := registry.Capability{Name: "m", Builder: "generic", Reversibility: registry.Reversible, AcceptsStdin: true}
	if err := e.ValidateBuilders([]registry.Capability{mutating}); err == nil {
		t.Error("expected an error: a mutating capability must not set accepts_stdin")
	}

	builtinCap := registry.Capability{Name: "p", Builder: "pwd", Reversibility: registry.ReadOnly, AcceptsStdin: true}
	if err := e.ValidateBuilders([]registry.Capability{builtinCap}); err == nil {
		t.Error("expected an error: a builtin has no stdin to wire and must not set accepts_stdin")
	}
}

// TestRun_AcceptsStdinCapabilityRefusesStandaloneWithoutInput confirms a
// standalone call to an AcceptsStdin capability with no positional input
// fails fast with a clear error instead of launching a subprocess that would
// block forever waiting on stdin that will never arrive.
func TestRun_AcceptsStdinCapabilityRefusesStandaloneWithoutInput(t *testing.T) {
	wc := lookupCapability(t, "wc")
	_, err := New().Run(context.Background(), wc, map[string]any{"lines": true}) // no paths
	if err == nil {
		t.Fatal("expected an error: wc has no paths and no piped input outside a pipeline")
	}
	if !strings.Contains(err.Error(), "piped input") {
		t.Errorf("error should explain the piped-input requirement, got: %v", err)
	}
}

// TestRun_GrepRefusesStandaloneWithoutPaths is the named-builder counterpart
// to the generic-builder case above: grep's "paths" is consumed entirely by
// buildGrep (a named builder, which ignores ArgRule when assembling argv), so
// missingPositionalInput must still find it via its ArgPositional marker —
// confirms a regression where grep's paths was tagged ArgNone, which made the
// standalone guard never fire and let a bare `grep(pattern: ...)` call run
// silently against an empty stdin instead of being refused.
func TestRun_GrepRefusesStandaloneWithoutPaths(t *testing.T) {
	grep := lookupCapability(t, "grep")
	_, err := New().Run(context.Background(), grep, map[string]any{"pattern": "x"}) // no paths
	if err == nil {
		t.Fatal("expected an error: grep has no paths and no piped input outside a pipeline")
	}
	if !strings.Contains(err.Error(), "piped input") {
		t.Errorf("error should explain the piped-input requirement, got: %v", err)
	}
}

// TestRun_LS executes ls against a temp directory and confirms real output.
func TestRun_LS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out, err := New().Run(context.Background(), lsCapability(t), map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("Run ls: %v", err)
	}
	if !strings.Contains(out, "marker.txt") {
		t.Errorf("ls output missing fixture file: %q", out)
	}
}
