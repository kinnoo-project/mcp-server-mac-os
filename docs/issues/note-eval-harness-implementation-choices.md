**note**

Implementation-level decisions made while building the eval harness
(`internal/evals`, `cmd/runevals`) that weren't pre-decided in the plan and
aren't captured elsewhere:

1. **`server.Connect` extracted as a shared, exported helper**
   (`internal/server/inprocess.go`). The integration test's `connectClient`
   already wired a real registry+engine+server to an in-memory MCP transport;
   rather than have the eval harness duplicate that wiring, it was factored
   into one exported function both call. This means the eval harness always
   talks to *exactly* the same code path the integration tests already cover —
   an eval failure can't be explained away by "the harness wired things up
   differently than production."

2. **`{{unique}}` prompt placeholder**, substituted once per case (not per
   turn) with a fresh random hex token before any turn's prompt is sent. Added
   because mutation cases create real, named resources (a real `/tmp`
   directory via `mkdir`) — without this, re-running the same case twice in a
   row would have the second run's `mkdir` legitimately refuse ("already
   exists"), which is correct *engine* behavior but would look like a harness
   flake, not a real model failure. This is a generic mechanism, not specific
   to any one case.

3. **Mutation cases self-clean via a scripted third "please undo that" turn**
   rather than via out-of-band test cleanup code. A live run of
   `mkdir_stages_then_confirms_then_undoes` or
   `write_setting_stages_then_confirms_then_undoes` really does create a
   directory / really does flip a real Finder preference when it reaches
   `execute` — the undo turn is what removes/restores it. This was verified
   manually after the first live run: no stray `/tmp/mcp-eval-*` directories,
   and `com.apple.finder AppleShowAllFiles` confirmed still unset afterward.
   Side benefit: this gives the harness real coverage of the `undo` tool,
   which the case set would otherwise have skipped.

4. **The system prompt sent to the model under evaluation is deliberately
   generic** ("You are a helpful assistant with access to tools for
   inspecting and managing this person's Mac.") and does NOT instruct the
   model to wait for confirmation before calling `execute`. Spelling that rule
   out in the eval's own system prompt would test whether the model follows
   an instruction *we* fed it, not whether the tool descriptions and design
   naturally produce the right behavior — which is the actual thing worth
   measuring.

5. **JSON case files, not YAML** — see
   `docs/issues/issue-need-eval-harness-for-tool-selection.md`'s Resolution
   section for the reasoning (matches the registry manifest convention; no
   stdlib YAML support).

First live run (2026-06-17, `claude-sonnet-4-6`, all 14 cases): 14/14 passed.
