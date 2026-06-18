**note**

Design and implementation decisions made while building the `pipeline` tool
(`internal/engine/pipeline.go`, `internal/server/pipeline.go`), beyond what's
already covered in README.md's [pipeline section](../../README.md#pipeline-composing-capabilities).

1. **Read-only, binary-backed stages only — deliberately, not as a stopgap.**
   Builtins (`pwd`, `largest_files`) have no subprocess to wire a pipe to;
   mutators (`mkdir`, `write_setting`) don't run in one step at all (they
   stage, then commit, then optionally undo). Restricting pipelines to
   read-only stages means there is **never** anything to roll back — keeping
   composition and the mutation phase's transactional machinery orthogonal,
   which was the design goal from the original composition discussion
   ([[composition-design-direction]] memory, and
   `issue-composition-and-transactional-rollback-limitations.md`).

2. **Sequential, buffered execution chosen over a true concurrent OS pipe.**
   A real unix pipe runs every stage concurrently with the kernel streaming
   bytes between them, so no stage's full output ever sits in memory at once.
   This implementation instead runs each stage to completion, captures its
   stdout (capped at `maxPipelineStageBytes`, 1 MiB), and feeds that buffer as
   the next stage's stdin. Simpler to implement correctly (no goroutines, no
   concurrent error/cancellation handling across stages) and exactly as
   correct at this project's scale: at most `MaxPipelineStages` (5) stages,
   each capped well below where concurrent streaming's memory advantage would
   actually matter. Worth revisiting only if a real use case needs
   dramatically larger intermediate data than 1 MiB.

3. **`AcceptsStdin` as a new registry field, not a runtime relaxation hack.**
   `wc`/`grep`'s `paths` was `required: true` specifically to prevent a
   standalone call from hanging forever on a stdin read that would never
   arrive (nothing wired stdin before pipelines existed). Rather than give
   `normalizeParams` two different required-ness contracts (standalone vs.
   pipeline), `paths` became honestly optional in the manifest, and a single
   new guard — `missingPositionalInput`, shared by `Run` and `RunPipeline` —
   refuses a standalone call with no input source instead of executing it.
   `AcceptsStdin` is engine-validated (`ValidateBuilders`), not
   registry-validated, since the registry package has no way to know which
   builder names are argv builders vs. builtins vs. mutators — that
   distinction is engine's alone.

4. **`sort`'s `key` parameter is a free-form string** (passed via `-k`)
   rather than a more structured field/direction pair, matching the
   project's existing `du`'s `max_depth` (`-d N`) precedent for a single
   valued flag rather than over-modeling a rarely-used option.

5. **The "nudge toward named operations first" lives in the tool description,
   verified by eval cases, not enforced by the engine.** The engine has no way
   to know "a named operation already covers this" — that's a tool-selection
   quality question, which the eval harness (added with `find_then_count_uses_pipeline`
   and `forbid_tools: ["pipeline"]` on several existing cases) is the
   mechanism for catching, not a runtime restriction.

6. **Two correctness bugs in the initial implementation, caught by PR review
   (see `docs/pr-reviews/pr5.md` for the full record):** (a) point 2 above's
   cap was, on first pass, mistakenly checked on *every* stage including the
   final one — the final stage's raw output is meant to flow straight to the
   same uncapped compaction path `Run` uses, so a single-stage or final-stage
   pipeline shouldn't behave differently from a standalone call. Fixed to
   only check `i < len(stages)-1`. (b) point 3's `missingPositionalInput`
   guard structurally only finds a parameter tagged `ArgPositional` — but
   `grep`'s named builder (`buildGrep`) ignores `Arg` entirely for its own
   argv assembly, so its manifest entry still needed `paths` tagged
   `positional` purely as a marker for the guard, even though that tag does
   nothing for argv building. Without it, a bare `grep(pattern: ...)` call
   with no `paths` silently ran against empty stdin instead of being refused.

See also: `docs/issues/issue-composition-and-transactional-rollback-limitations.md`
(item 1, composition, is now resolved by this work; item 2, multi-step
*mutation* plans, remains open and is a distinct, harder problem).
