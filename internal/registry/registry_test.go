// registry_test.go verifies two things: that the real embedded manifests load
// and are well-formed, and that the structural validator rejects every class of
// malformed capability it promises to catch.
//
// The malformed-capability tests use New (not Load) so each invariant can be
// exercised in isolation against a hand-built capability, following the
// table-driven, hermetic style used across this codebase.
package registry

import (
	"testing"
)

// validCapability returns a minimal, schema-valid capability that individual
// test cases mutate to violate exactly one invariant at a time.
func validCapability() Capability {
	return Capability{
		Name:          "demo",
		Summary:       "A demo capability.",
		Category:      "filesystem",
		Binary:        "ls",
		Reversibility: ReadOnly,
		Risk:          RiskNone,
		Builder:       "generic",
		Params: []ParamSpec{
			{Name: "path", Type: TypePath, Description: "A path.", Arg: ArgRule{Kind: ArgPositional}},
		},
	}
}

// TestLoad_EmbeddedManifests confirms the manifests shipped in the binary parse
// and pass validation, and that the ls capability is present and well-formed.
func TestLoad_EmbeddedManifests(t *testing.T) {
	r, err := Load()
	if err != nil {
		t.Fatalf("Load() embedded manifests: %v", err)
	}
	if r.Len() == 0 {
		t.Fatal("Load() returned an empty registry")
	}

	ls, ok := r.Lookup("ls")
	if !ok {
		t.Fatal("expected an 'ls' capability in the embedded manifests")
	}
	if ls.Binary != "ls" {
		t.Errorf("ls.Binary = %q, want \"ls\"", ls.Binary)
	}
	if ls.Builder != "generic" {
		t.Errorf("ls.Builder = %q, want \"generic\"", ls.Builder)
	}
	if ls.Reversibility != ReadOnly {
		t.Errorf("ls.Reversibility = %q, want %q", ls.Reversibility, ReadOnly)
	}
	if ls.Risk != RiskNone {
		t.Errorf("ls.Risk = %q, want %q", ls.Risk, RiskNone)
	}
	if len(ls.Params) == 0 {
		t.Fatal("ls capability has no parameters")
	}
	// The path parameter must be a positional so the engine places it after the
	// "--" terminator.
	var foundPath bool
	for _, p := range ls.Params {
		if p.Name == "path" {
			foundPath = true
			if p.Type != TypePath {
				t.Errorf("ls.path type = %q, want %q", p.Type, TypePath)
			}
			if p.Arg.Kind != ArgPositional {
				t.Errorf("ls.path arg kind = %q, want %q", p.Arg.Kind, ArgPositional)
			}
		}
	}
	if !foundPath {
		t.Error("ls capability is missing its 'path' parameter")
	}
}

// TestReadOnlyInvariant asserts the phase-wide guarantee that every shipped
// capability is read-only. This replaces the old tools.go-grep test with a
// registry-driven check.
func TestReadOnlyInvariant(t *testing.T) {
	r, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	for _, c := range r.All() {
		if c.Reversibility != ReadOnly {
			t.Errorf("capability %q has reversibility %q; only read_only is allowed this phase",
				c.Name, c.Reversibility)
		}
	}
}

// TestList filters by category and confirms All == List("").
func TestList(t *testing.T) {
	r, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if got := r.List("filesystem"); len(got) == 0 {
		t.Error("List(\"filesystem\") returned nothing; expected ls and friends")
	}
	if got := r.List("does-not-exist"); len(got) != 0 {
		t.Errorf("List on unknown category returned %d capabilities, want 0", len(got))
	}
	if len(r.All()) != len(r.List("")) {
		t.Error("All() should equal List(\"\")")
	}
}

// TestNew_AcceptsValid confirms a minimal valid capability — including a
// parameter with no generic arg mapping (as a named builder would use) — passes
// validation.
func TestNew_AcceptsValid(t *testing.T) {
	c := validCapability()
	// A parameter consumed by a named builder may omit its arg mapping entirely.
	c.Params = append(c.Params, ParamSpec{Name: "pattern", Type: TypeString, Description: "x"})
	if _, err := New([]Capability{c}); err != nil {
		t.Fatalf("New rejected a valid capability: %v", err)
	}
}

// TestNew_Rejects drives each validation branch with a capability that violates
// exactly one invariant, asserting Load/New fails fast.
func TestNew_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *Capability)
	}{
		{"empty name", func(c *Capability) { c.Name = "" }},
		{"missing summary", func(c *Capability) { c.Summary = "" }},
		{"missing category", func(c *Capability) { c.Category = "" }},
		{"unknown reversibility", func(c *Capability) { c.Reversibility = "sometimes" }},
		{"unknown risk", func(c *Capability) { c.Risk = "spicy" }},
		{"empty param name", func(c *Capability) { c.Params[0].Name = "" }},
		{"unknown param type", func(c *Capability) { c.Params[0].Type = "blob" }},
		{"enum without list", func(c *Capability) { c.Params[0].Type = TypeEnum; c.Params[0].Enum = nil }},
		{"unknown arg kind", func(c *Capability) { c.Params[0].Arg.Kind = "sideways" }},
		{"flag kind without flag", func(c *Capability) {
			c.Params[0].Arg = ArgRule{Kind: ArgFlag} // no Flag token
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCapability()
			tc.mutate(&c)
			if _, err := New([]Capability{c}); err == nil {
				t.Fatalf("expected validation error for %q, got nil", tc.name)
			}
		})
	}
}

// TestNew_RejectsDuplicateNames covers the cross-capability uniqueness check,
// which needs two capabilities rather than a single mutation.
func TestNew_RejectsDuplicateNames(t *testing.T) {
	a, b := validCapability(), validCapability()
	if _, err := New([]Capability{a, b}); err == nil {
		t.Fatal("expected an error for duplicate capability names")
	}
}

// TestNew_RejectsDuplicateParams covers per-capability parameter uniqueness.
func TestNew_RejectsDuplicateParams(t *testing.T) {
	c := validCapability()
	c.Params = append(c.Params, ParamSpec{Name: "path", Type: TypePath, Arg: ArgRule{Kind: ArgPositional}})
	if _, err := New([]Capability{c}); err == nil {
		t.Fatal("expected an error for duplicate parameter names")
	}
}
