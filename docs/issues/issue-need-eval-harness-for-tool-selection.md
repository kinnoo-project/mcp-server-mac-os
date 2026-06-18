**issue**

The existing test suite (see `docs/TESTS.md`) proves the engine and protocol are
correct *given a specific tool call*. It says nothing about whether **the model
picks the right tool call** for a given natural-language ask — that's a different
failure mode, and unit/integration tests structurally can't catch it because they
bypass the model entirely. (The `largest_files` 12-loop incident, documented in
project history, is exactly this kind of gap: the engine was fine, the model's
tool-selection strategy wasn't.)

No eval harness exists yet. This issue records the recommendation for when and
how to build one, so the decision isn't re-derived from scratch later.

## Evals — when, and how

That's the case for evals: a corpus of `(prompt → expected tool call/outcome)`
pairs scored by actually running the prompt through a model with this server
attached, not asserting against Go code.

**When it starts to make sense**, roughly in this order of urgency:
1. **As soon as a domain tool's description gets non-trivial** — i.e., now. With
   10 operations and one mutating one, ambiguity is already possible (e.g., would
   "make a directory if there isn't one" correctly map to `mkdir`, or would the
   model hallucinate a `force` param?).
2. **Before adding more domains** — once there's a `network` or `application`
   tool alongside `filesystem`, cross-domain selection errors become possible and
   worth catching before they ship.
3. **Mandatory before any irreversible-operation or multi-step-plan work** —
   that's where a wrong tool choice has real cost, so eval coverage should gate
   that work, not follow it.

**How I'd write them**, concretely:
- **Format**: a JSON/YAML fixture file, one case per natural-language prompt,
  each with the model+server wired up via the SDK's in-memory transport (the same
  harness `integration_test.go` already uses — no new infra). Each case declares:
  - the prompt,
  - the expected tool name + operation (or an explicit "should refuse"
    expectation),
  - for mutating ops, whether it should reach `execute` only after an
    affirmative confirmation turn (never auto-confirm),
  - an assertion on the *final* observable state (e.g. "directory exists" /
    "directory does not exist"), not just the tool-call shape — this catches a
    model that picks the right tool but wrong params.
- **Scope per eval**: keep it to single-turn tool selection plus the mutation
  confirmation behavior. Multi-turn conversational evals (the user saying "no"
  mid-flow, or asking to undo three messages later) are a second, harder tier —
  write those once the first tier is stable.
- **Run cadence**: not in the regular `go test` loop (these call a real model,
  cost money, and are non-deterministic) — a separate `eval` target/script, run
  before merging changes to tool descriptions or the manifest, and periodically
  against new model versions to catch behavioral drift.
- **Scoring**: deterministic where possible (did the right operation run, did the
  filesystem end up in the right state — these are checkable without an LLM
  judge). Reserve an LLM-judge step only for things that are genuinely hard to
  assert mechanically, like "was the preview shown to the user actually clear."

**status: open — recommendation only, no harness built yet**
