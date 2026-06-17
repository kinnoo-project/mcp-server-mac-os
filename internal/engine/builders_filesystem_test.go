// builders_filesystem_test.go pins the argument vectors produced by the find and
// grep named builders (the tricky cases: OR-grouped name expressions, the "--"
// guard, and grep's default-on line numbering) and covers the extension safety
// check. Inputs are run through the real normalizer so the tests reflect the
// actual request path.
package engine

import (
	"reflect"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// capFromRegistry loads a named capability from the embedded registry.
func capFromRegistry(t *testing.T, name string) registry.Capability {
	t.Helper()
	r, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	c, ok := r.Lookup(name)
	if !ok {
		t.Fatalf("capability %q not found", name)
	}
	return c
}

// build normalizes raw against the capability and runs its registered builder,
// returning the assembled argv.
func build(t *testing.T, name string, raw map[string]any) ([]string, error) {
	t.Helper()
	c := capFromRegistry(t, name)
	norm, err := normalizeParams(c, raw)
	if err != nil {
		return nil, err
	}
	b, ok := lookupBuilder(c.Builder)
	if !ok {
		t.Fatalf("no builder for %q", name)
	}
	return b(c, norm)
}

func TestBuildFind_Golden(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want []string
	}{
		{
			"type and extensions",
			map[string]any{"path": "/tmp", "type": "f", "extensions": []any{"png", "jpg"}},
			[]string{"/tmp", "-type", "f", "(", "-iname", "*.png", "-o", "-iname", "*.jpg", ")"},
		},
		{
			"name pattern and depth",
			map[string]any{"path": "/x", "name_pattern": "*.go", "max_depth": float64(2)},
			[]string{"/x", "-maxdepth", "2", "(", "-name", "*.go", ")"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := build(t, "find", tc.raw)
			if err != nil {
				t.Fatalf("build find: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildFind_Guards(t *testing.T) {
	// A path beginning with "-" would be parsed by find as an expression.
	if _, err := build(t, "find", map[string]any{"path": "-exec"}); err == nil {
		t.Error("expected rejection of dash-leading find path")
	}
	// An extension carrying shell/glob metacharacters must be rejected.
	if _, err := build(t, "find", map[string]any{"path": "/tmp", "extensions": []any{"png;rm -rf /"}}); err == nil {
		t.Error("expected rejection of unsafe extension")
	}
}

func TestBuildGrep_Golden(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want []string
	}{
		{
			"dash pattern gets default line numbers",
			map[string]any{"pattern": "-v", "paths": []any{"/a"}},
			[]string{"-I", "-n", "-e", "-v", "--", "/a"},
		},
		{
			"count summary suppresses line numbers",
			map[string]any{"pattern": "x", "paths": []any{"/a"}, "count": true},
			[]string{"-I", "-c", "-e", "x", "--", "/a"},
		},
		{
			"full option set",
			map[string]any{
				"pattern": "x", "paths": []any{"/a"},
				"recursive": true, "ignore_case": true,
				"include": "*.go", "context": float64(2), "max_count": float64(3),
			},
			[]string{"-I", "-r", "-i", "-n", "-m", "3", "-C", "2", "--include=*.go", "-e", "x", "--", "/a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := build(t, "grep", tc.raw)
			if err != nil {
				t.Fatalf("build grep: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsSafeExtension(t *testing.T) {
	for _, ok := range []string{"png", "JPG", "tar-gz", "a_b", "c4"} {
		if !isSafeExtension(ok) {
			t.Errorf("%q should be safe", ok)
		}
	}
	for _, bad := range []string{"", "p/g", "p;g", "p g", "p*", "thisextensioniswaytoolong"} {
		if isSafeExtension(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
