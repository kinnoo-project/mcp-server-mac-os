# PR #5 review — Mvp2/impl3

2026-06-18, mode: fix

(internal/engine/pipeline.go) maxPipelineStageBytes is documented as an intermediate-stage cap, but the current check applies to every stage (including the final stage, and even a 1-stage pipeline). This makes pipeline([find...]) fail on large outputs even though standalone Run would just compact the result, and it contradicts the comments/README describing the cap as intermediate-only.

**fixed**
The cap now only applies when `i < len(stages)-1` (this stage's output is actually feeding another stage). The final stage (and a single-stage pipeline) goes straight to the same `formatRunResult`/`compactOutput` path `Run` uses, which has no pre-compaction cap of its own. Split the test into `TestRunPipeline_IntermediateSizeCapEnforced` (non-final, still enforced) and `TestRunPipeline_FinalStageNotSizeCapped` (final/single-stage, not enforced). Commit `913da88`.

---

(internal/registry/manifests/filesystem.json) grep now allows omitting paths (to read piped stdin), but paths is still marked with arg.kind = "none". The standalone-call guard (missingPositionalInput) only checks positional args, so calling filesystem.grep directly with just pattern will run against empty stdin (Go wires nil stdin to /dev/null) instead of being refused as described in the README/manifest text. Mark paths as positional so standalone calls without paths are rejected, while non-first pipeline stages can still omit it.

**fixed**
Changed grep's `paths` to `arg.kind: "positional"`. `buildGrep` (a named builder) ignores `Arg` entirely for argv assembly, so this is purely a structural marker for `missingPositionalInput` to find — documented that nuance in both `registry/types.go`'s `ArgPositional` doc comment and `pipeline.go`'s. Added `TestRun_GrepRefusesStandaloneWithoutPaths` as the named-builder regression test (mirrors the existing `wc` generic-builder one). Commit `913da88`.

---

(internal/server/pipeline.go) RunPipeline already prefixes its errors with pipeline:. Wrapping it again with errorResult("pipeline: %v", err) duplicates the prefix (e.g. pipeline: pipeline: stage 2 ...), making errors noisier and harder to read.

**fixed**
Changed to `errorResult("%v", err)` since `RunPipeline`'s errors already carry the `pipeline: stage N (...): ...` prefix. Commit `913da88`.
