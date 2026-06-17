**issue**
Two capabilities the architecture does not yet provide, flagged for awareness (not defects — deferred scope):

1. **Server-side composition** — a single capability that bundles several binaries into one transactional unit with shared state. This is out of scope for the read-only spine (Slices A–C).

2. **Transactional multi-step mutations with rollback** — the `plan`/`commit` path and the `internal/transaction` (and `internal/snapshot`) machinery were explicitly deferred to the later mutation phase. Only when that lands does "chain operations, and roll back if a mid-sequence step fails" become a first-class server concern.

Nuance worth recording: because step chaining is currently LLM-driven, data passes between steps **through the model's context** — the model reads step 1's text output and then forms step 2's arguments. There is no direct server-side pipe from one capability's output into another's input. For read-only inspection this is perfectly adequate; a direct, atomic, rollback-safe pipeline is only needed for guaranteed transactional sequences, which is the future mutation phase, not the read-only spine.

**fixed**
