// registry.go loads capability manifests, enforces their structural invariants
// at startup, and serves them through read-only accessors.
//
// Loading is fail-fast by design: a malformed manifest is a programming/authoring
// error that must surface the moment the server boots, never as a confusing
// runtime failure on a user's request. Load therefore returns an error that
// main() is expected to treat as fatal.
//
// Validation here is deliberately limited to invariants the registry OWNS —
// closed enums, unique names, required fields, parameter shape. It does NOT
// verify that a capability's Builder resolves to a real argv builder: builders
// live in internal/engine, and making the registry aware of them would invert
// the intended dependency direction (engine -> registry, never the reverse).
// The engine validates builder resolvability when it is wired up.
package registry

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
)

// manifestFS embeds every domain manifest so the binary is fully self-contained
// and needs no manifest files on disk at runtime.
//
//go:embed manifests/*.json
var manifestFS embed.FS

// Registry is the in-memory, validated set of capabilities. It is immutable
// after construction and safe for concurrent reads. Construct it via Load (for
// the embedded manifests) or New (for an explicit slice, used in tests).
type Registry struct {
	// caps holds capabilities in stable manifest order, which the discovery
	// menu and List preserve for deterministic output.
	caps []Capability
	// byName indexes caps by Capability.Name for O(1) Lookup.
	byName map[string]Capability
}

// Load parses and validates every embedded domain manifest and returns the
// resulting Registry. It fails fast: any parse or validation error is returned
// and should be treated as fatal by the caller.
func Load() (*Registry, error) {
	caps, err := parseManifests(manifestFS, "manifests")
	if err != nil {
		return nil, err
	}
	return New(caps)
}

// New validates an explicit slice of capabilities and indexes them. It exists
// alongside Load so tests can exercise validation against hand-built (including
// deliberately malformed) capabilities without needing on-disk fixtures.
func New(caps []Capability) (*Registry, error) {
	if err := validate(caps); err != nil {
		return nil, err
	}
	byName := make(map[string]Capability, len(caps))
	for _, c := range caps {
		byName[c.Name] = c
	}
	return &Registry{caps: caps, byName: byName}, nil
}

// parseManifests reads every *.json file under dir in fsys and concatenates the
// capability arrays they contain, in lexical filename order for determinism.
func parseManifests(fsys fs.FS, dir string) ([]Capability, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("registry: reading manifest dir %q: %w", dir, err)
	}
	var all []Capability
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".json" {
			continue
		}
		full := path.Join(dir, e.Name())
		data, err := fs.ReadFile(fsys, full)
		if err != nil {
			return nil, fmt.Errorf("registry: reading manifest %q: %w", full, err)
		}
		var caps []Capability
		if err := json.Unmarshal(data, &caps); err != nil {
			return nil, fmt.Errorf("registry: parsing manifest %q: %w", full, err)
		}
		all = append(all, caps...)
	}
	return all, nil
}

// Lookup returns the capability with the given name and whether it exists.
func (r *Registry) Lookup(name string) (Capability, bool) {
	c, ok := r.byName[name]
	return c, ok
}

// List returns the capabilities in the given category, in manifest order. An
// empty category returns every capability. The returned slice is a copy, so
// callers cannot mutate the registry's backing data.
func (r *Registry) List(category string) []Capability {
	out := make([]Capability, 0, len(r.caps))
	for _, c := range r.caps {
		if category == "" || c.Category == category {
			out = append(out, c)
		}
	}
	return out
}

// All returns every capability in manifest order (a copy).
func (r *Registry) All() []Capability {
	return r.List("")
}

// Len reports how many capabilities are registered.
func (r *Registry) Len() int { return len(r.caps) }

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// validReversibility and validRisk are the closed enum sets the registry owns.
// Declaring them as data keeps validation a single table lookup and makes the
// allowed values self-evident.
var validReversibility = map[Reversibility]bool{
	ReadOnly: true, Reversible: true, Compensatable: true, Irreversible: true,
}

var validRisk = map[Risk]bool{
	RiskNone: true, RiskLow: true, RiskMedium: true, RiskHigh: true,
}

var validParamType = map[ParamType]bool{
	TypeBool: true, TypeString: true, TypeInt: true, TypePath: true,
	TypeStringList: true, TypePathList: true, TypeEnum: true,
}

// validArgKind includes the empty kind, which means "this parameter has no
// generic argv mapping" (a named builder consumes it).
var validArgKind = map[ArgKind]bool{
	"": true, ArgFlag: true, ArgValuedFlag: true, ArgPositional: true, ArgNone: true,
}

// validate enforces every structural invariant the registry guarantees to its
// consumers. It returns the first violation found, with a message that names the
// offending capability/parameter so authoring mistakes are easy to locate.
func validate(caps []Capability) error {
	seen := make(map[string]bool, len(caps))
	for _, c := range caps {
		if c.Name == "" {
			return fmt.Errorf("registry: capability with empty name (summary %q)", c.Summary)
		}
		if seen[c.Name] {
			return fmt.Errorf("registry: duplicate capability name %q", c.Name)
		}
		seen[c.Name] = true

		if c.Summary == "" {
			return fmt.Errorf("registry: capability %q: summary is required", c.Name)
		}
		if c.Category == "" {
			return fmt.Errorf("registry: capability %q: category is required", c.Name)
		}
		if !validReversibility[c.Reversibility] {
			return fmt.Errorf("registry: capability %q: unknown reversibility %q", c.Name, c.Reversibility)
		}
		if !validRisk[c.Risk] {
			return fmt.Errorf("registry: capability %q: unknown risk %q", c.Name, c.Risk)
		}
		// auto_commit bypasses the human-approval gate, so it is confined to
		// low-stakes mutations: it is meaningless on a read-only capability
		// (there is nothing to commit) and forbidden on medium/high-risk ones
		// (those must keep the stage→execute confirmation step).
		if c.AutoCommit {
			if c.Reversibility == ReadOnly {
				return fmt.Errorf("registry: capability %q: auto_commit is not valid on a read_only capability", c.Name)
			}
			if c.Risk == RiskMedium || c.Risk == RiskHigh {
				return fmt.Errorf("registry: capability %q: auto_commit requires risk none or low, got %q", c.Name, c.Risk)
			}
		}
		if err := validateParams(c); err != nil {
			return err
		}
	}
	return nil
}

// validateParams checks the parameter schema of a single capability: unique
// names, known types, enum/flag consistency.
func validateParams(c Capability) error {
	seen := make(map[string]bool, len(c.Params))
	for _, p := range c.Params {
		if p.Name == "" {
			return fmt.Errorf("registry: capability %q: parameter with empty name", c.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("registry: capability %q: duplicate parameter %q", c.Name, p.Name)
		}
		seen[p.Name] = true

		if !validParamType[p.Type] {
			return fmt.Errorf("registry: capability %q, param %q: unknown type %q", c.Name, p.Name, p.Type)
		}
		// A TypeEnum parameter is meaningless without its allowlist.
		if p.Type == TypeEnum && len(p.Enum) == 0 {
			return fmt.Errorf("registry: capability %q, param %q: enum type requires a non-empty enum list", c.Name, p.Name)
		}
		if !validArgKind[p.Arg.Kind] {
			return fmt.Errorf("registry: capability %q, param %q: unknown arg kind %q", c.Name, p.Name, p.Arg.Kind)
		}
		// Flag-bearing kinds must carry the literal flag token to emit.
		if (p.Arg.Kind == ArgFlag || p.Arg.Kind == ArgValuedFlag) && p.Arg.Flag == "" {
			return fmt.Errorf("registry: capability %q, param %q: arg kind %q requires a flag token", c.Name, p.Name, p.Arg.Kind)
		}
	}
	return nil
}
