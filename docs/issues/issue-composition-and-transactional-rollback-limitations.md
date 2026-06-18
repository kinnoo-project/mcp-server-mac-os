**issue**
Two capabilities the architecture does not yet provide, flagged for awareness (not defects — deferred scope):

1. **Server-side composition** — a single capability that bundles several binaries into one transactional unit with shared state. This is out of scope for the read-only spine (Slices A–C).

2. **Transactional multi-step mutations with rollback** — ~~the `plan`/`commit` path and the `internal/transaction` (and `internal/snapshot`) machinery were explicitly deferred to the later mutation phase.~~ **Partially resolved (Phase 2, 2026-06-17):** `internal/transaction` now exists (a generic, thread-safe, TTL one-shot token store) and backs a single-step stage → execute → undo flow, proved on `mkdir`. What remains open is specifically **multi-step** rollback: chaining several mutations as one unit with a best-effort + report failure policy (stop on first failure, report what completed, let the user `undo` the completed reversible steps) is still future work — see the Roadmap in `README.md`. There is also no `internal/snapshot` package; undo today is value/inverse-command based (e.g. `mkdir` ↔ `rmdir`), not snapshot-based, and that has been sufficient so far.

Nuance worth recording: because step chaining is currently LLM-driven, data passes between steps **through the model's context** — the model reads step 1's text output and then forms step 2's arguments. There is no direct server-side pipe from one capability's output into another's input. For read-only inspection this is perfectly adequate; a direct, atomic, rollback-safe pipeline is only needed for guaranteed transactional sequences (item 2, still open for the multi-step case) or for composition (item 1, still fully open).

**status: item 1 open; item 2 partially fixed (single-step), multi-step rollback still open**
