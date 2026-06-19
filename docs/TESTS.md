# Regression tests currently run

Run via `go test ./...` (plus `-race` on the store), organized bottom-up through
the architecture:

| Layer | What's actually being checked |
|---|---|
| **`internal/registry`** | Manifests parse and load; structural validation rejects malformed capabilities (duplicate names, unknown enum/type values, a flag-kind param missing its flag token); the new `TestRiskClassificationInvariant` checks every mutating capability carries non-`none` risk. |
| **`internal/policy`** | Binary resolution only ever returns a path under `/bin`, `/sbin`, `/usr/bin`, `/usr/sbin`; path-separator injection and rogue-substitution attempts are rejected. |
| **`internal/engine`** | Per-type parameter coercion (tilde expansion, enum/required checks, unknown-key rejection); the generic builder's flag → `--` → positional ordering; `find`/`grep`'s irregular named-builder grammars; `largest_files`' ranking. For mutation: `stageMkdir`'s forward/inverse/preview values, its existing-path and dash-leading-path guardrails, and a real stage → run-forward → run-inverse round trip against a temp directory; `stageWriteSetting`'s forward/inverse/preview values for both the unset-key case and the prior-value-capture case, its refusal to stage when the existing value isn't a plain boolean, its refusal of a setting name absent from the allowlist, a data sanity check that every curated entry has non-empty domain/key/label, and a real stage → run-forward → run-inverse round trip via the real `defaults` binary against a **synthetic allowlist entry pointing at a disposable temp file** (never a real curated domain — see Safety note below); and `stageSendMail`'s validation (rejects no recipients, an address with no `@`, an empty subject, a missing or directory attachment path), its count-prefixed argv layout (`["-e", script, subject, body, recipientCount, recipients..., attachments...]` — the count is what lets two variable-length lists share one flat argv with no delimiter), its verbatim preview text (including the attachment-filename line, present only when attachments exist), and that `Inverse` is always `nil` (irreversible) — **no test ever executes the `Forward` command**, since that would send a real email (see Safety note below). For pipelines (`pipeline_test.go`): `SupportsPipeline`'s eligibility rule against the real manifest (read-only argv-builder capabilities eligible; builtins and mutators rejected); a real two-stage `find`→`wc` round trip; `MaxPipelineStages` enforcement; the first-stage-missing-input guard (refuses rather than hangs); a failing middle stage aborting with its exit code; and the intermediate-size cap, split into `TestRunPipeline_IntermediateSizeCapEnforced` (a non-final stage exceeding the lowered cap aborts) and `TestRunPipeline_FinalStageNotSizeCapped` (the cap does NOT apply to a pipeline's last/only stage — its raw output goes straight to the same uncapped compaction path `Run` uses). `TestRun_AcceptsStdinCapabilityRefusesStandaloneWithoutInput` (generic-builder, `wc`) and `TestRun_GrepRefusesStandaloneWithoutPaths` (named-builder counterpart — a PR review caught that `grep`'s `paths` needed `arg.kind: "positional"` for this guard to find it at all) cover the standalone-hang guard; `TestValidateBuilders_AcceptsStdin` covers its boot-time precondition (only read-only, argv-builder capabilities may set `accepts_stdin`). For `search_mail` (`builtins_mail_test.go`): `parseMdlsOutput`'s pure parsing against canned `mdls`-format text (including the `(null)`-missing-attribute case and that attribute order isn't assumed), `splitNonEmptyLines`, the required-`query` guard, and a real `mdfind` call with a query engineered to match nothing (see Safety note below). |
| **`internal/transaction`** | The token store's contract: round-trip, prefix/uniqueness, one-shot consumption, TTL expiry, opportunistic purging of expired entries on `Put` (so an abandoned-token workload can't grow the store without bound), and (under `-race`) safety under concurrent `Put`/`Take`. |
| **`internal/server`** | Behavioral tests drive every capability through the real domain-tool handler against a hermetic fixture tree; the in-process integration test drives the *actual* MCP protocol: tool listing across all three domain tools (`filesystem`, `preferences`, `application-mail`) plus `execute`/`undo`/`pipeline`, the full `mkdir` stage→execute→undo round trip, a **stage-only** `write_setting` call against a real curated setting (asserting a token+preview come back, deliberately never calling `execute` — see Safety note below), a real `find`→`wc` pipeline round trip over the protocol, a real `search_mail` no-match call, a **stage-only** `send_mail` call (asserting the irreversibility warning appears in the preview, never calling `execute` — see Safety note below), structured errors for bad operations/tokens/pipeline stages (including a mutator or an unknown capability name as a stage), and a drift check (`TestDefaultsAllowlist_MatchesManifestEnum`) that the manifest's `setting` enum and the engine's `defaultsAllowlist` map name exactly the same settings. |

### Safety note: `send_mail` tests never send a real email

`send_mail` is irreversible, so unlike every other mutator there is no
disposable/synthetic target to test against — the only safe boundary is
**never executing `StagedPlan.Forward`** at all. Every test (engine-level
`mutate_mail_test.go` and the protocol-level integration test) inspects only
the staged plan's fields; none calls `RunCommand`, `execute`, or otherwise
runs the underlying `osascript` command.

### Safety note: `search_mail` tests never touch real mail content

The one real `mdfind` call in the test suite (`builtins_mail_test.go` and the
protocol-level integration test) uses a query string engineered to match
nothing (a long, obviously-arbitrary token), confirmed empirically to return
zero results regardless of what mail exists on the machine running the
test — so the real subprocess path is exercised without ever reading or
printing real personal email content.

### Safety note: `write_setting` tests never touch real preferences

`write_setting`'s curated settings point at real domains (`com.apple.finder`,
`com.apple.dock`, `NSGlobalDomain`, `com.apple.screencapture`). Any test that
actually *writes* through `write_setting` therefore uses a synthetic
`defaultsAllowlist` entry pointing at an absolute path under `t.TempDir()` —
confirmed that `defaults` treats an arbitrary file path as a one-off domain —
inserted for the duration of one test and removed via `t.Cleanup`. The only test
that touches a real curated setting (`finder_show_hidden_files`) stops at
**staging**, which only performs a read-only `defaults read` probe; it never
calls `execute`, so the developer's or CI machine's actual Finder setting is
never written.

This is a fairly classic test pyramid for this kind of system: pure-data/pure-function
layers get exhaustive unit coverage, and the top layer gets a smaller number of
high-value, protocol-real integration tests rather than re-testing every
capability through MCP. There's no live macOS smoke test (piping JSON into the
binary by hand) because the server exits on stdin EOF before flushing — that's a
known, accepted limitation (`docs/issues/issue-stdio-smoke-test-unreliable.md`).

What this suite does **not** cover: whether a model picks the right tool call for
a given natural-language prompt. That is a model-selection concern, not an
engine-correctness one — see the separate eval harness below.

## Eval harness (`internal/evals`, NOT part of `go test ./...`)

Built per `docs/issues/issue-need-eval-harness-for-tool-selection.md` (now
resolved). Unlike everything above, this calls a real Anthropic model and is
therefore **not free, not deterministic, and not run automatically** — it's a
separate command (`go run ./cmd/runevals`, or `-dry-run` for the zero-cost
validation-only path) documented in full in README.md's
[Evals](../README.md#evals) section.

What IS unit-tested without any network call or API key (so it does run under
plain `go test ./...`):

| File | What's being checked |
|---|---|
| `internal/evals/case_test.go` | JSON case loading: single-turn (`prompt`) vs. multi-turn (`turns`) resolve correctly; both-set and neither-set are rejected; duplicate IDs across files are caught; load order is sorted/deterministic; non-`.json` files in the cases directory are ignored. |
| `internal/evals/runner_test.go` | `CheckExpectation`'s pure assertion logic against hand-built `TurnOutcome` values: tool/operation matching, the `forbid_tools` auto-confirm guard (the harness's core safety check), and `text_contains` substring checks — all independent of the live agent loop in `runner.go`, which can only be exercised against a real model. |

The live agent loop itself (`runner.go`'s `RunAll`/`runCase`/`runTurn`) is
exercised only by actually running `go run ./cmd/runevals` against the 18
cases in `evals/cases/*.json` — there is no mock-model unit test for it, since
the entire point is measuring real model behavior. One case
(`find_then_count_uses_pipeline`) specifically checks the model reaches for
`pipeline` when no named operation covers the request; several others assert
`forbid_tools: ["pipeline"]` to catch the model over-using it when a named
operation already suffices.

`evals/cases/mail.json` carries the suite's strictest safety constraint:
`send_mail_stages_only_never_executes` is single-turn with `forbid_tools:
["execute"]` and is never followed by a confirmation turn, so a live eval run
can never reach `execute` for the one irreversible capability in the
registry. `search_mail_selection` dictates the exact (nonsense) search term
in its prompt so the live run is safe to actually execute without surfacing
real email content to the Anthropic API.
