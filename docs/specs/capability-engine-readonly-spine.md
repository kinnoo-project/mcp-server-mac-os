# Spec: Capability Engine — Read-Only Spine

> Features 1–3 of the MVP in `docs/ideas/macos-mcp-capability-engine.md`. This phase rebuilds the read-only functionality on the new registry + engine architecture. **No mutation, no `plan`/`commit`/`undo`, no transaction/snapshot machinery** — those are the next phase.
>
> The existing `src/` code is an MVP that proved viability; it is **not a constraint**. This phase is free to redesign or replace it wholesale. The eight read-only file operations are kept only as the concrete content that exercises the engine — their exact param shapes and output are open to redesign, and tests assert *correct behavior*, not fidelity to the old handlers.

## Objective

Replace the eight hand-written, one-tool-per-binary read-only handlers with a **data-driven capability registry** executed by a **fixed engine**, exposing a small fixed MCP tool surface (Pattern A). Success = read-only operations work correctly through the new architecture, and adding a new read-only operation becomes a JSON manifest entry, not new Go code.

This phase is the proof that the registry can express *real* tools and that the engine's seams are right, before any irreversible mutation logic depends on them.

**User stories**
- As an agent, I call `list_capabilities` and see every available operation as data, then `query(capability="ls", params={...})` to run a read-only operation.
- As a maintainer, I add a read-only capability by adding a manifest entry + (only if its argv grammar is complex) a named builder — never a new MCP tool.
- As a security reviewer, the input guardrails from `.claude/rules/darwin-execution.md` live in exactly one place (engine + policy), not duplicated across handlers.

## Tech Stack

- **Go 1.26.3** (per `go.mod`).
- **`github.com/modelcontextprotocol/go-sdk` v1.4.1** — *pinned; do not upgrade this phase.* (CLAUDE.md's v1.6.0 claim is incorrect and will be corrected — see Boundaries.)
- **JSON manifests** via stdlib `encoding/json` — **no new dependency.** JSON is the MCP wire format, so manifests share the protocol's data model.
- `go:embed` for bundling manifests into the binary.

## Commands

```
Format:   go fmt ./...
Vet:      go vet ./...
Deps:     go mod tidy
Test:     go test ./...
Test -v:  go test -v ./...
Build:    go build -o bin/macos-darwin-mcp ./cmd/macos-darwin-mcp
```

> The build path changes from `main.go` to `./cmd/macos-darwin-mcp`. CLAUDE.md's current `go build ... main.go` is already broken (main.go lives in `src/`); this phase fixes it.

## Project Structure

Idiomatic restructure (replaces the flat `src/*.go` `package main` layout):

```
cmd/macos-darwin-mcp/
  main.go                  → wiring only: load registry → build engine → register tools → Run over stdio
internal/registry/
  types.go                 → Capability, ParamSpec, ArgRule, enums (Reversibility, ParamType, ArgKind)
  registry.go              → Load(fs), Validate(), Lookup(name), List(category), Describe(name)
  manifests/               → embedded JSON, one file per domain
    filesystem.json        →   ls, file, find, stat, du, wc, grep, pwd
  registry_test.go
internal/engine/
  executor.go              → runCommand, compactOutput, formatRunResult, expandUserPath
  validate.go              → ParamSpec-driven validation (required, type, enum, path expansion)
  argbuild.go              → ArgBuilder registry: buildGeneric + named builders (buildFind, buildGrep, buildPwd)
  engine.go                → Run(ctx, cap, params) → validate → build argv → resolve → exec → format
  *_test.go
internal/policy/
  binaries.go              → allowedBinDirs, resolveBinary trust boundary
  binaries_test.go
internal/server/
  tools.go                 → the 3 engine tools + handlers (query, list_capabilities, describe_capability)
  menu.go                  → builds the embedded capability menu string for tool descriptions
  tools_test.go            → behavioral tests on a hermetic fixture tree
docs/specs/                → this file
```

`src/` is deleted after migration. Package dependency direction (enforces dependency inversion):

```
server ─→ engine ─→ policy
   └────→ registry ←─ engine
```

`registry` is pure data + validation (no `os/exec`). `engine` holds behavior (execution, arg-building) and depends on `registry` *types* only. `policy` is the trust boundary (which binaries may run). `server` is the MCP adapter.

## Code Style

Manifest entry (declarative; the generic builder handles argv). Each domain file is a JSON array of capabilities:

```json
{
  "name": "ls",
  "summary": "List a directory or describe a single file.",
  "category": "filesystem",
  "binary": "ls",
  "reversibility": "read_only",
  "risk": "none",
  "builder": "generic",
  "params": [
    { "name": "path", "type": "path", "required": false, "default": ".",
      "description": "Dir/file to list; supports ~.", "arg": { "kind": "positional" } },
    { "name": "all",            "type": "bool", "description": "Include dotfiles.",        "arg": { "kind": "flag", "flag": "-A" } },
    { "name": "long",           "type": "bool", "description": "Long format.",             "arg": { "kind": "flag", "flag": "-l" } },
    { "name": "human_readable", "type": "bool", "description": "Human sizes (with long).", "arg": { "kind": "flag", "flag": "-h" } },
    { "name": "recursive",      "type": "bool", "description": "Recurse subdirs.",         "arg": { "kind": "flag", "flag": "-R" } }
  ]
}
```

Core types (registry):

```go
type Reversibility string

const (
	ReadOnly      Reversibility = "read_only"
	Reversible    Reversibility = "reversible"    // declared now; unused until mutation phase
	Compensatable Reversibility = "compensatable"
	Irreversible  Reversibility = "irreversible"
)

// Risk is a coarse hazard label the policy layer reads to decide what needs
// confirmation/escalation. All four tiers are defined now (medium/high unused
// this phase) so manifests never need backfilling; the registry rejects values
// outside this set. The set is extensible by editing this one place.
type Risk string

const (
	RiskNone   Risk = "none"   // pure read, no sensitive exposure: ls, pwd, stat, file, du, wc
	RiskLow    Risk = "low"    // reads file contents / enumerates broadly: grep, find
	RiskMedium Risk = "medium" // reserved: reversible/compensatable mutations
	RiskHigh   Risk = "high"   // reserved: irreversible/bulk
)

type ParamSpec struct {
	Name        string    `json:"name"`
	Type        ParamType `json:"type"` // bool|string|int|path|string_list|path_list|enum
	Required    bool      `json:"required,omitempty"`
	Default     any       `json:"default,omitempty"`
	Enum        []string  `json:"enum,omitempty"`
	Description string    `json:"description"`
	Arg         ArgRule   `json:"arg,omitempty"` // {kind: flag|valued_flag|positional|none, flag: "-x"}
}

type Capability struct {
	Name          string        `json:"name"`
	Summary       string        `json:"summary"`
	Category      string        `json:"category"`
	Binary        string        `json:"binary"`
	Reversibility Reversibility `json:"reversibility"`
	Risk          Risk          `json:"risk"`
	Builder       string        `json:"builder"` // "generic" (default) | "find" | "grep" | "pwd"
	Params        []ParamSpec   `json:"params"`
}
```

Engine arg-building contract (data-driven where possible, named-builder escape hatch where the argv grammar is complex):

```go
// ArgBuilder turns validated params into an argv suffix (excluding the binary).
type ArgBuilder func(c registry.Capability, in map[string]any) ([]string, error)

var builders = map[string]ArgBuilder{
	"generic": buildGeneric, // walks ParamSpecs in order; flags then `--` then positionals
	"find":    buildFind,    // OR-grouped name expression + dash-leading-path guard
	"grep":    buildGrep,    // -e pattern, --include=, -I, summary-mode logic
	"pwd":     buildPwd,     // builtin: no binary; resolves os.Getwd() (see note)
}
```

> **Design decision (the crux of feature 3):** 5 of 8 capabilities (`ls`, `file`, `stat`, `du`, `wc`) are fully declarative via `buildGeneric`. `find` and `grep` have bespoke argv assembly (OR-groups, `-e`, `--include=`) that is awkward to fully declare — they get named builders that read validated params from the registry. `pwd` has no binary, so it uses a `builtin` builder that resolves `os.Getwd()` and bypasses `exec`, keeping the registry the single source of truth. Every capability names a builder; most name `generic`. This is "data-driven where it pays, code where the grammar demands it."

The 3 engine tools share the v1.4.1 handler contract:

```go
func queryHandler(ctx context.Context, _ *mcp.CallToolRequest, in QueryArgs) (*mcp.CallToolResult, any, error)

type QueryArgs struct {
	Capability string         `json:"capability" jsonschema:"required,description=Capability name from list_capabilities."`
	Params     map[string]any `json:"params,omitempty" jsonschema:"description=Capability-specific parameters; validated at call time."`
}
```

> Pattern-A consequence: `query`'s schema is generic (`params` is an **open object**); per-capability validation happens at call time and returns structured errors, not schema rejections. The **embedded menu** (capability names + summaries, generated at startup by `server/menu.go`) is injected into `query`'s and `list_capabilities`' descriptions so the model picks valid capabilities with zero discovery round-trips.
>
> **`ParamSpec` is the single source of truth, rendered three ways** (so nothing drifts): (a) human text in the embedded menu, (b) a real **JSON Schema** returned by `describe_capability` giving the model full per-capability rigor on demand, and (c) — later — a native-tool schema if a capability is promoted to Pattern B. The `params` open object is intentional, not a gap: rigor lives in runtime validation + discovery, not in `query`'s static schema (enumerating all capabilities there would defeat Pattern A).

Tests stay table-driven and hermetic (a `makeTempTree`-style fixture and a `textOf` content extractor).

## Testing Strategy

- **Framework:** stdlib `testing`, table-driven. Tests live beside their package.
- **Behavioral correctness (highest priority):** each capability is verified to produce *correct, useful* results on a hermetic fixture tree — e.g., `find` with `extensions:[png,jpg]` returns the images and excludes the `.txt`; `grep` no-match surfaces exit code 1 as data, not error. These assert behavior, **not** byte-identity with the old MVP handlers (which we are free to improve on).
- **registry:** loads & validates embedded JSON; rejects malformed manifests (missing required field, duplicate name, unknown `builder`, unknown `type`, unknown `risk`, unknown `reversibility`). Startup validation is itself tested. `ParamSpec` → JSON Schema rendering is unit-tested (same machinery `describe_capability` uses).
- **engine:** golden-argv tests per capability (validated params → exact argv); validation rejects (unsafe `find` extension, unknown `find -type`, dash-leading `find` path, empty `grep` pattern); executor (`compactOutput` truncation; non-zero exit treated as data, not error).
- **policy:** `resolveBinary` resolves into trusted dirs; rejects path separators and out-of-tree binaries.
- **server:** `query` routes to the right capability; unknown capability → structured `not_found`; `list_capabilities` shape; `describe_capability` returns a valid JSON Schema for the capability's params.
- **Read-only invariant:** assert **every registered capability has `reversibility: read_only`** this phase (registry-driven; replaces the old `tools.go`-grep test).
- **Coverage expectation:** every arg-builder and every validation branch exercised.

## Boundaries

- **Always:** route all logging to stderr (clean stdout JSON-RPC framing); invoke binaries via `exec.CommandContext` with positional `[]string` (never a shell); place `--` before positional/path args; validate every param through `ParamSpec` before argv assembly; resolve binaries through `policy` only; keep tests green before committing.
- **Ask first:** adding dependencies; changing the manifest schema shape; upgrading the go-sdk; changing the engine-tool surface (the 3 tools); deleting `src/` (do it as the final migration step once the new path is green).
- **Never:** wire a mutating binary into a `read_only` capability; pass any string to `sh`/`bash`/`zsh`/`eval`; print to stdout outside the MCP loop; weaken a behavioral test to make migration "pass" — the new capabilities must actually work.

## Success Criteria

1. `go build ./cmd/macos-darwin-mcp` produces a working stdio server on the new layout.
2. All 8 read-only operations are available via `query` and behave correctly, verified by behavioral tests on a hermetic fixture tree.
3. The MCP tool surface is exactly 3 tools: `query`, `list_capabilities`, `describe_capability`.
4. `list_capabilities` returns all 8 capabilities as data; `query`/`list_capabilities` descriptions contain the embedded menu.
5. Registry validates at startup; a deliberately malformed manifest causes a clear fatal error (tested).
6. Input guardrails (dash-leading `find` path, safe extensions, output compaction, binary allowlist) exist in exactly one place each (`engine`/`policy`), with no duplication.
7. `internal/registry`, `internal/engine`, `internal/policy`, `internal/server` each have passing unit tests.
8. CLAUDE.md reconciled: SDK version (v1.4.1), build command (`./cmd/macos-darwin-mcp`), `src/` references, and manifest format (JSON) updated.
9. `src/` removed.

## Implementation Cadence (preview of Plan phase — step-wise, not one-shot)

These three features are one cohesive unit (none is independently testable), so they're specced together but built in gated vertical slices:

- **Slice A** — registry types + JSON loader + startup validation + the `ls` capability. *Verify: registry loads & validates; malformed manifest fails fast.*
- **Slice B** — engine (`executor`, `buildGeneric`) + `policy` + `query` runs `ls`. *Verify: `query(ls)` returns a correct listing of the fixture tree.*
- **Slice C** — remaining 7 capabilities (`buildFind`, `buildGrep`, `buildPwd`). *Verify: behavioral suite green across all 8.*
- **Slice D** — `list_capabilities` + `describe_capability` + embedded menu; delete `src/`; reconcile CLAUDE.md. *Verify: 3-tool surface; discovery lists all 8.*

## Open Questions

*(None blocking. The Slice-B spike below is a confirmation, not a decision.)*

- **Slice-B spike:** confirm jsonschema-go v0.4.2 emits a sane open `{"type":"object"}` for `map[string]any`. If it emits nothing usable, attach an explicit object schema (a few lines) — **not** a JSON-string param, which is the brittle path.

## Resolved Decisions (from review)

- Layout: idiomatic `cmd/` + `internal/` restructure. · SDK: stay on v1.4.1, fix the doc.
- Manifests: **JSON** (MCP wire format; zero new deps).
- transaction/snapshot packages: **deferred** to the mutation phase.
- `pwd`: **`builtin` builder** (no binary), keeping the registry as the single source of truth.
- The MVP `src/` is **not a constraint** — free to redesign; behavioral tests replace any byte-for-byte parity requirement.
- `query.params`: **open object**, with `ParamSpec` as the single source rendered to human text + JSON Schema (`describe_capability`); rigor lives in runtime validation + discovery.
- Risk: **four-tier enum** (`none`/`low`/`medium`/`high`) defined now, validated by the registry, extensible by editing one place; read-only set assigned `none` except `grep`/`find` = `low`.
