# Regression tests currently run

Run via `go test ./...` (plus `-race` on the store), organized bottom-up through
the architecture:

| Layer | What's actually being checked |
|---|---|
| **`internal/registry`** | Manifests parse and load; structural validation rejects malformed capabilities (duplicate names, unknown enum/type values, a flag-kind param missing its flag token); the new `TestRiskClassificationInvariant` checks every mutating capability carries non-`none` risk. |
| **`internal/policy`** | Binary resolution only ever returns a path under `/bin`, `/sbin`, `/usr/bin`, `/usr/sbin`; path-separator injection and rogue-substitution attempts are rejected. |
| **`internal/engine`** | Per-type parameter coercion (tilde expansion, enum/required checks, unknown-key rejection); the generic builder's flag → `--` → positional ordering; `find`/`grep`'s irregular named-builder grammars; `largest_files`' ranking; and for mutation — `stageMkdir`'s forward/inverse/preview values, its existing-path and dash-leading-path guardrails, and a real stage → run-forward → run-inverse round trip against a temp directory. |
| **`internal/transaction`** | The token store's contract: round-trip, prefix/uniqueness, one-shot consumption, TTL expiry, and (under `-race`) safety under concurrent `Put`/`Take`. |
| **`internal/server`** | Behavioral tests drive every capability through the real domain-tool handler against a hermetic fixture tree; the in-process integration test drives the *actual* MCP protocol (tool listing, the full `mkdir` stage→execute→undo round trip, structured errors for bad operations/tokens). |

This is a fairly classic test pyramid for this kind of system: pure-data/pure-function
layers get exhaustive unit coverage, and the top layer gets a smaller number of
high-value, protocol-real integration tests rather than re-testing every
capability through MCP. There's no live macOS smoke test (piping JSON into the
binary by hand) because the server exits on stdin EOF before flushing — that's a
known, accepted limitation (`docs/issues/issue-stdio-smoke-test-unreliable.md`).

What this suite does **not** cover: whether a model picks the right tool call for
a given natural-language prompt. That is a model-selection concern, not an
engine-correctness one, and needs a separate eval harness — see
`docs/issues/issue-need-eval-harness-for-tool-selection.md`.
