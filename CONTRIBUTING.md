# Contributing to mcp-server-mac-os

Thanks for your interest in contributing! This project turns natural language into
safe, native macOS actions over the Model Context Protocol. Its architecture is
deliberately built so that **most new operations are a JSON manifest entry, not new
Go code** — which makes adding to it unusually approachable. This guide explains how
to get set up, how the codebase is organized, and how to land a change.

By participating, you agree to uphold a welcoming, respectful, and harassment-free
environment for everyone.

---

## Ways to contribute

- 🐛 **Report a bug** — open an issue with steps to reproduce, what you expected,
  and what happened (include the offending tool output, with any `[stderr]` block).
- ✨ **Add a capability** — a new operation in an existing domain (often just a
  manifest edit) or a whole new domain.
- 🧪 **Add eval cases** — prompts that prove the model picks the right tool and
  respects the confirmation gate.
- 📝 **Improve docs** — the README, [`docs/architecture.md`](docs/architecture.md),
  or the design notes under [`docs/`](docs/).
- 💡 **Discuss design** — open an issue to propose or debate an approach before
  writing code; for anything security-adjacent, this is strongly preferred.

If your change is large or changes a public behavior, please open an issue to
discuss it first so we can agree on direction before you invest the time.

---

## Development setup

### Prerequisites

- **macOS 13 Ventura or newer** (Apple Silicon or Intel). Ventura is the floor —
  the server relies on modern `x-apple.systempreferences:` panes and does no
  per-version fallback.
- **[Go 1.26+](https://go.dev/dl/)** (matching `go.mod`).
- An MCP-aware client (**Claude Code** or **Claude Desktop**) if you want to test
  end-to-end against a real model.

### Clone, build, run the checks

```bash
git clone https://github.com/kinnoo-project/mcp-server-mac-os.git
cd mcp-server-mac-os

go mod tidy
go build -o bin/macos-darwin-mcp ./cmd/macos-darwin-mcp
```

### The verification pipeline

Run this before every commit — a green pipeline is required for any PR:

```bash
gofmt -l ./cmd ./internal        # must print nothing
go vet ./...
go test ./...
go test -race ./internal/transaction/...   # the concurrent token store
```

See [`docs/TESTS.md`](docs/TESTS.md) for what the suite covers. The test suite is
safe to run repeatedly: `write_setting` tests use a disposable temp file, never
your real Finder/Dock preferences, and no test sends mail, places a call, or
prints.

### Evals (optional, for model-behavior changes)

If your change affects tool descriptions, the operation menu, or the confirmation
flow, run the eval harness — it puts a real model in the loop:

```bash
go run ./cmd/runevals -dry-run        # free: validates cases + resolves schemas
export ANTHROPIC_API_KEY=sk-ant-...   # live run makes billed API calls (a few cents)
go run ./cmd/runevals
```

See [`docs/architecture.md#evals`](docs/architecture.md#evals) for how it works.

---

## How the codebase is organized

The design is a **capability registry + a fixed engine**: operations are described
as *data* (JSON manifests), not hand-written per-operation tools.

| Package | Responsibility |
| --- | --- |
| `cmd/macos-darwin-mcp/` | Entry point — wiring only: load registry → build server → serve over stdio. |
| `internal/registry/` | The capability catalog: types + embedded JSON manifests + fail-fast validation. Pure data; no `os/exec`, no MCP. |
| `internal/engine/` | Execution: parameter validation, argv assembly (generic + named builders + in-process builtins), and the subprocess runner. |
| `internal/policy/` | The trust boundary — which binaries may run, and from where. |
| `internal/transaction/` | The stage ↔ execute/undo bridge: a generic, thread-safe, TTL, one-shot token store. |
| `internal/server/` | The MCP adapter — domain tools + `execute`/`undo`/`pipeline` handlers. |

Dependency direction is strictly one-way: `server → engine → policy`, all
depending only on `registry` types. See
[`docs/architecture.md`](docs/architecture.md) for the full map and diagrams.

---

## Adding a capability

A **read-only** operation is usually just data:

1. Add an entry to the relevant manifest in `internal/registry/manifests/`,
   describing the binary, parameters (`ParamSpec`), and how each parameter maps to
   an argument (`ArgRule`). Most operations use the **generic builder** — a fully
   declarative mapping. Irregular grammars use a small **named builder**, and
   questions a single command cannot answer use an in-process **builtin**.
2. If you need a named builder or builtin, add it under `internal/engine/` and
   register it. The server fails fast at boot if a manifest references a builder
   that does not exist, so wiring mistakes surface immediately.
3. Add tests and, where it proves tool selection, an eval case.

A **mutating** operation additionally implements a **mutator** that returns a
`StagedPlan` with a human-readable `Preview`, a `Forward` command, and an
`Inverse` (or `nil` if genuinely irreversible). It is then reachable through the
stage → execute → undo gate automatically. See
[`docs/architecture.md#mutating-operations-stage--execute--undo`](docs/architecture.md#mutating-operations-stage--execute--undo).

> ⚠️ **Security-sensitive additions** (anything touching `write_setting`'s
> allowlist, AppleScript, URL schemes, or SQL) require extra care and a reviewed
> Go change, not just a data edit. Read
> [`.claude/rules/darwin-execution.md`](.claude/rules/darwin-execution.md) and the
> [safety model](docs/architecture.md#why-this-server-is-safe-to-expose) first, and
> please open an issue to discuss before implementing.

---

## Coding standards

These are the project's non-negotiable axioms (full text in
[`CLAUDE.md`](CLAUDE.md) and [`.claude/rules/`](.claude/rules/)):

- **No shell, ever.** Invoke utilities via `exec.CommandContext` with an explicit
  `[]string` — never `sh -c`, never string concatenation. Neutralize option
  injection (the `--` terminator for `osascript`; reject dash-leading values where
  there is no terminator).
- **`os.Stdout` is for JSON-RPC only.** All logs and diagnostics go to
  `os.Stderr`.
- **Validate input before assembling arguments.** Strict allowlists, typed fields,
  deterministic argument order.
- **Mutations stage first.** Never run a destructive action on the first call;
  compute the forward *and* inverse up front.
- **Self-documenting code.** Every file opens with a doc comment; every exported
  (and every non-trivial unexported) function/type carries a Go doc comment
  explaining purpose, contract, and failure modes — not a restatement of the
  signature. Comment the *why* behind non-obvious logic.
- **Keep docs current.** Update `README.md`, `docs/architecture.md`, and
  `docs/TESTS.md` as features and tests change; record design notes, deferred
  scope, and bugs under `docs/issues/` using the conventions in `CLAUDE.md` §8.

---

## Submitting a pull request

1. **Branch** off `main` (don't commit directly to it).
2. **Make focused commits** with clear messages explaining the *why*.
3. **Run the full verification pipeline** above — never push a red build.
4. **Update the docs** affected by your change.
5. **Open a PR** describing what changed and why, and link any related issue.
   Automated review comments (e.g. from Copilot) are welcome signal — address them
   or explain why not.

---

## License

This project is licensed under the **GNU Affero General Public License v3.0** (see
[LICENSE](LICENSE)). By contributing, you agree that your contributions will be
licensed under the same terms.
