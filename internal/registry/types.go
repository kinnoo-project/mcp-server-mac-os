// Package registry defines the data model for macOS "capabilities" and loads,
// validates, and serves them to the rest of the MCP server.
//
// # Architecture role
//
// A capability is a single, declaratively-described operation the server can
// perform (for example "ls" or "find"). Capabilities are DATA — JSON manifests
// embedded into the binary — not hand-written Go tools. This package is the
// single source of truth for that data: it parses the manifests, enforces
// structural invariants at startup (fail fast), and exposes read-only accessors
// (Lookup/List/Describe) to the engine and MCP server layers.
//
// This package is intentionally behaviour-free with respect to execution: it
// knows nothing about os/exec, argv assembly, or the MCP transport. Those
// concerns live in internal/engine and internal/server, which depend on the
// types declared here. Keeping the registry a pure data+validation layer is
// what lets new operations be added as a manifest entry rather than new code
// (see docs/specs/capability-engine-readonly-spine.md).
//
// types.go specifically declares the on-disk/in-memory shape of a capability
// and the closed enums its fields may take. The loading and validation logic
// lives in registry.go.
package registry

// Reversibility classifies how (and whether) a capability's effects can be
// undone. It is declared in full now even though this phase ships only
// read-only capabilities, so that the mutation phase can populate it without a
// schema migration. The policy layer will later read this to decide what
// requires staging, snapshots, or refusal.
type Reversibility string

const (
	// ReadOnly operations have no side effects and never need rollback.
	ReadOnly Reversibility = "read_only"
	// Reversible operations can be undone by replaying a captured inverse
	// (e.g. restoring a prior `defaults` value). Unused this phase.
	Reversible Reversibility = "reversible"
	// Compensatable operations are undone by a compensating action rather than
	// a true inverse (e.g. restoring a file from ~/.Trash). Unused this phase.
	Compensatable Reversibility = "compensatable"
	// Irreversible operations cannot be undone and must be escalated, optionally
	// guarded by a snapshot backstop. Unused this phase.
	Irreversible Reversibility = "irreversible"
)

// Risk is a coarse hazard label the policy layer reads to decide what needs
// confirmation or escalation. All four tiers are defined now (medium/high are
// reserved for the mutation phase) so manifests never need backfilling. The
// registry rejects any value outside this set; the set is extended by editing
// this one place.
type Risk string

const (
	// RiskNone is a pure read with no sensitive exposure (ls, pwd, stat, file, du, wc).
	RiskNone Risk = "none"
	// RiskLow reads file contents or enumerates broadly and so could surface
	// secrets or large trees (grep, find).
	RiskLow Risk = "low"
	// RiskMedium is reserved for reversible/compensatable mutations.
	RiskMedium Risk = "medium"
	// RiskHigh is reserved for irreversible or bulk mutations.
	RiskHigh Risk = "high"
)

// ParamType is the closed set of value types a capability parameter may take.
// The engine's validator uses this to coerce and bounds-check incoming JSON
// before any argv is assembled, which is a core injection guardrail.
type ParamType string

const (
	// TypeBool is a presence flag (true appends a flag, false omits it).
	TypeBool ParamType = "bool"
	// TypeString is an arbitrary string value.
	TypeString ParamType = "string"
	// TypeInt is an integer value (rendered into argv as its decimal form).
	TypeInt ParamType = "int"
	// TypePath is a filesystem path; the engine expands a leading ~ and places
	// it after a "--" argv terminator to defeat flag injection.
	TypePath ParamType = "path"
	// TypeStringList is an ordered list of strings.
	TypeStringList ParamType = "string_list"
	// TypePathList is an ordered list of paths, expanded like TypePath.
	TypePathList ParamType = "path_list"
	// TypeEnum is a string constrained to ParamSpec.Enum.
	TypeEnum ParamType = "enum"
)

// ArgKind tells the generic argument builder how a parameter contributes to the
// command line. Capabilities whose grammar does not fit these kinds use a named
// builder instead (see Capability.Builder) and may leave Arg empty.
type ArgKind string

const (
	// ArgFlag appends ArgRule.Flag when a bool parameter is true.
	ArgFlag ArgKind = "flag"
	// ArgValuedFlag appends ArgRule.Flag followed by the parameter's value.
	ArgValuedFlag ArgKind = "valued_flag"
	// ArgPositional appends the parameter's value(s) as positional operands
	// after the "--" terminator. A named builder ignores this for argv
	// assembly (it builds argv itself), but should still mark its
	// AcceptsStdin-relevant parameter ArgPositional: the engine's
	// missingPositionalInput helper uses this kind purely as a structural
	// marker for "this is the slot a piped stage's input fills," independent
	// of whether a generic or named builder is in play (see grep's "paths").
	ArgPositional ArgKind = "positional"
	// ArgNone means the parameter takes part in validation but contributes no
	// argv directly (a named builder consumes it).
	ArgNone ArgKind = "none"
)

// ArgRule describes how a single parameter maps onto the command line for the
// generic builder. It is ignored by named builders, which assemble argv
// themselves from the validated parameter map.
type ArgRule struct {
	// Kind selects the mapping strategy (flag, valued_flag, positional, none).
	Kind ArgKind `json:"kind"`
	// Flag is the literal flag token emitted for ArgFlag/ArgValuedFlag
	// (e.g. "-A"). Unused for positional/none kinds.
	Flag string `json:"flag,omitempty"`
}

// ParamSpec is the declarative schema for one capability parameter. It is the
// single source of truth for that parameter, rendered three ways without
// duplication: human text in the embedded discovery menu, a JSON Schema served
// by describe_capability, and (later) a native MCP tool schema if a capability
// is promoted. The engine validates incoming values against this spec before
// building argv.
type ParamSpec struct {
	// Name is the JSON key callers supply inside the query params object.
	Name string `json:"name"`
	// Type is the value's closed-set type (see ParamType).
	Type ParamType `json:"type"`
	// Required, when true, causes validation to fail if the parameter is absent.
	Required bool `json:"required,omitempty"`
	// Default is substituted when an optional parameter is omitted. Its concrete
	// type should match Type (e.g. "." for a path, false for a bool).
	Default any `json:"default,omitempty"`
	// Enum constrains a TypeEnum value to this allowlist. Required for TypeEnum,
	// ignored otherwise.
	Enum []string `json:"enum,omitempty"`
	// Description is human-facing documentation surfaced through discovery.
	Description string `json:"description"`
	// Arg maps this parameter onto the command line for the generic builder.
	Arg ArgRule `json:"arg,omitempty"`
}

// Capability is the complete declarative description of one operation the
// server can perform. A domain manifest file is a JSON array of these.
//
// The combination of Binary + Builder + Params is everything the engine needs
// to validate input and assemble a safe, fully-tokenized command — without any
// per-capability Go code in the common case.
type Capability struct {
	// Name is the unique identifier callers pass to the query tool. Must be
	// unique across the whole registry.
	Name string `json:"name"`
	// Summary is a one-line description used in the discovery menu.
	Summary string `json:"summary"`
	// Category groups related capabilities (e.g. "filesystem") and drives the
	// optional filter on list_capabilities.
	Category string `json:"category"`
	// Binary is the bare name of the system utility to invoke (e.g. "ls"). The
	// policy layer resolves it to a trusted absolute path. May be empty for
	// builtin builders that synthesise output without a subprocess.
	Binary string `json:"binary"`
	// Reversibility classifies undo semantics (this phase: always ReadOnly).
	Reversibility Reversibility `json:"reversibility"`
	// Risk is the coarse hazard label consumed by the policy layer.
	Risk Risk `json:"risk"`
	// Builder selects the argv-assembly strategy: "generic" for the declarative
	// builder, or a named builder ("find", "grep", "pwd", ...) for grammars the
	// generic builder cannot express. Empty is treated as "generic".
	Builder string `json:"builder"`
	// Params is the ordered parameter schema. Order is significant for the
	// generic builder, which emits flags in declaration order.
	Params []ParamSpec `json:"params"`
	// AcceptsStdin marks a capability whose underlying binary reads from
	// standard input when its positional file argument(s) are omitted (the
	// classic unix filter idiom: wc, grep, sort, head, ...). It exists so this
	// capability can serve as a non-first stage of a pipeline, receiving the
	// previous stage's output as its input instead of a named file.
	//
	// This is engine-validated, not registry-validated: only a read-only
	// capability backed by an argv builder (never a builtin or a mutator) may
	// set this, and the registry package has no way to know which builder
	// names resolve to which kind — see engine.ValidateBuilders. A capability
	// invoked standalone (outside a pipeline) with this set and no positional
	// argument is refused outright rather than executed, because there is
	// nothing to wire to its stdin and it would otherwise hang forever
	// waiting for input that will never arrive.
	AcceptsStdin bool `json:"accepts_stdin,omitempty"`
}
