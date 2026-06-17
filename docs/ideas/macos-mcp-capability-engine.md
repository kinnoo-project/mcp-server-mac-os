# macOS MCP Capability Engine

> A registry-driven, transactional engine that lets an agent accomplish macOS system and application tasks through intent — safely, reversibly, and auditably enough to eventually trust unattended.

## Problem Statement

**How might we let an agent accomplish any macOS system or application task through intent — safely and reversibly enough to trust unattended — so that reaching for a raw terminal becomes the exception, not the default?**

The MVP registers one strongly-typed MCP tool per Darwin utility (8 read-only tools: `ls`, `pwd`, `file`, `grep`, `du`, `find`, `stat`, `wc`). Two structural problems block the vision:

1. **Scaling.** "One tool per operation" grows linearly and never finishes. Large tool lists also degrade LLM tool-selection accuracy and eat context. (The C-replaced-assembly analogy actually argues *against* this: a compiler is a grammar + code generator, not one function per instruction.)
2. **Differentiation.** The read-only file tools substantially overlap the Bash tool an agent already has. The defensible value — safe mutation, rollback, audit, and app/system integration — is exactly what isn't built yet (the `Stage`/`Commit` pattern in `CLAUDE.md` is currently spec-only).

## Recommended Direction

Re-architect around a **capability registry + a fixed engine** (the "compiler" model):

- **The registry is the grammar** — capabilities are *data* (YAML manifests), not code. Adding an operation = adding a manifest entry, not a new Go tool.
- **The engine is the code generator + runtime** — a small, fixed set of MCP tools that validate, preview, execute, journal, and roll back, generically, by reading manifests.

This solves scaling (MCP surface stays fixed; mapping language→intent is the *client model's* job, not yours — one parameterized capability covers infinitely many phrasings) and refocuses effort on the differentiated layer (safe, reversible mutation).

**Pattern A (chosen): capabilities as data, discovered at runtime.** The 6 engine tools are the only tools the LLM sees. Capabilities are surfaced as *data* via discovery — like a database exposing fixed verbs (`SELECT`/`INSERT`) + an `information_schema`, not one function per table. *Mitigation #1 (chosen):* embed a compact capability menu (names + summaries) directly in the engine tools' static descriptions, so the common case needs zero discovery round-trips; full schemas come from `describe_capability` only when needed. Pattern B (generate a native tool per capability) is the opposite horn of the same trade-off and reads the same registry — reserved as a later optimization to *promote* the 3–4 highest-frequency capabilities.

### Engine tools (fixed surface — never grows)

| Tool | Path | Role |
|---|---|---|
| `list_capabilities` | discovery | Names + summary + risk + reversibility (optional `category` filter) |
| `describe_capability` | discovery | Full param schema + examples for one capability |
| `query` | read-only | Execute a read-only capability immediately (the current 8 tools live here) |
| `plan_action` | mutate · stage | Validate, **dry-run preview**, capture pre-state, materialize inverse, score risk, persist `PendingTransaction`, return `req_…` token + preview. **Executes nothing.** |
| `commit_action` | mutate · commit | Execute staged token, write journal entry w/ concrete inverse, return undo handle |
| `undo_action` | rollback | Replay a committed transaction's stored inverse |

(Optional 7th for the autonomous end-state: `list_transactions` — audit/history.)
The **plan → commit gap is the confirmation point** — dry-run is structural, not a bolted-on feature.

### Capability manifests (registry as data)

- **Layout:** one YAML per domain (`files.yaml`, `defaults.yaml`, `processes.yaml`), bundled via Go `embed`, **validated at startup (fail fast)**. File layout is an authoring concern; it collapses into one in-memory registry.
- **Fields:** `name`, `binary`, `params` (typed schema), `reversibility` (`reversible | compensatable | irreversible`), `risk`, `capture` (pre-state to snapshot), `inverse`.
- **Inverse = a materialized capability invocation**, not special code. The manifest declares *what pre-state to capture* and *which capability inverts it, as a template*; the engine materializes concrete values into the journal at commit. (`trash_file` → `restore_from_trash`; `write_default` → `write_default`/`delete_default` depending on whether the key existed.) Inverses get the same validation, preview, and risk-scoring as everything else.

### Reversibility model (tiered; honest about limits)

- `reversible` → save inverse op (e.g., prior `defaults` value).
- `compensatable` → route through `~/.Trash` / copy-aside.
- `irreversible`/bulk → wrap in an **APFS snapshot bracket** as a backstop, GC'd on successful commit or short TTL (never accumulate). Snapshots are copy-on-write: ~0 bytes at creation, cost = data changed/deleted since; `tmutil localsnapshot` auto-thins under disk pressure. Restore is whole-volume (a sledgehammer), so snapshots are a backstop, not the primary per-file undo.
- Truly irreversible non-FS ops (process kill, network) → **escalate, never fake an undo.**

### Failure & clarification (fail closed, fail loud)

`plan_action` returns structured outcomes, not just success/error:
- `not_found` (not built) → nearest matches **+ log the miss to a `capability-gaps` file = prioritized roadmap**.
- `blocked` (policy/TCC privacy) → actionable reason (e.g., "grant Full Disk Access").
- `precondition_failed` (runtime state wrong) → specific reason, pre-mutation.
- `ambiguous` / `needs_params` → return candidates / missing args; the engine **enforces** the clarifying question rather than trusting the model to ask. Scope ambiguity is handled by the dry-run preview.

**Invariant:** never silently degrade to ungated raw execution — responses steer the model away from running the raw command via its Bash tool.

## Key Assumptions to Validate

- [ ] **Multi-step protocol round-trips.** MCP clients reliably handle `plan → commit` (and discovery) handshakes — test with the actual target clients early; some hosts fumble multi-step tool flows.
- [ ] **Manifest-driven capture/inverse feels clean.** Build the thinnest end-to-end slice — `read_default` → `write_default` → `undo_action` — first. If declaring capture + inverse in YAML is ergonomic there, the architecture holds; if awkward, learn it on one capability, not thirty.
- [ ] **Snapshot privileges.** `fs_snapshot`/`tmutil` behave acceptably in the target runtime without elevated privileges. If they need entitlements, snapshots move from "core mechanism" to "optional backstop."
- [ ] **Discovery overhead is acceptable** with mitigation #1 (embedded menu) — the model finds capabilities without excessive round-trips.

## MVP Scope

**In:**
1. Capability registry + engine (`internal/registry`, `internal/engine`, `internal/transaction`, `internal/snapshot`, `internal/policy`).
2. The 6 engine tools, Pattern A + embedded-menu descriptions.
3. Migrate the existing 8 read-only tools into registry entries reachable via `query` (proves the registry can express real tools; shrinks `src/` duplication).
4. `Stage`/`Commit` + journal (currently spec-only).
5. **3–4 mutating capabilities across the tiers** to prove the model end-to-end:
   - `trash_file` (compensatable → Trash)
   - `write_default` / `read_default` / `delete_default` (reversible → save prior value)
   - one bulk `delete_under` (irreversible-tier → snapshot bracket)
   - each implements `simulate()` (dry-run).
6. Structured failure outcomes + `capability-gaps` logging.

**Out (this phase):** app/system integration (Calendar, Notes, Reminders via EventKit/`osascript`) — deferred until the trust core is proven.

## Not Doing (and Why)

- **A single generic `run_command` executor** — pushes correctness onto the LLM (hallucinated flags) and makes security review unbounded.
- **More read-only file tools as a breadth play** — overlaps the Bash tool; the project's weakest differentiation. Future breadth belongs in the mutating + app layer.
- **Promising rollback for everything** — dishonest; irreversible non-FS ops get escalation, not a fake undo.
- **`/force` bypass (collapse plan+commit, skip confirmation)** — backlogged. Caution for the ticket: *"authenticate as root"* is the wrong gate (running the server as root is a huge blast radius and almost nothing here needs it). Prefer an explicit config allowlist or OS auth prompt (Touch ID / Authorization Services).
- **Pattern B (generated per-capability tools)** — reserved as a later optimization for hot capabilities; reads the same registry.

## Open Questions

- Where does the transaction store live — in-memory only (lost on restart) or persisted (survives restart, supports `undo` across sessions)? The autonomous end-state probably wants persistence + a journal on disk.
- How long do `PendingTransaction` tokens live before expiry, and how are snapshot brackets GC'd (TTL vs. commit-driven)?
- For Pattern A discovery, what's the right size/shape of the embedded menu before it bloats the engine tool descriptions?
- Shortcuts.app as a ready-made app-integration registry (`shortcuts run`) — promising enough to reorder the app-integration phase earlier?
