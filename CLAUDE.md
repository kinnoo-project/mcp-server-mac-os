# Comprehensive macOS Darwin MCP Server - Unified Engineering Manual

## 1. Project Vision & Lifecycle Goal
This project provides a highly scalable, enterprise-grade Model Context Protocol (MCP) server that safely translates natural language intent into any native macOS (Darwin) CLI configuration pattern. The core goal is absolute system flexibility paired with structural, programmatic guardrails.

## 2. Technical Stack Context
- **Language Layer**: Go 1.26+ (Idiomatic, strictly typed concurrency structures)
- **Official SDK**: `github.com/modelcontextprotocol/go-sdk/mcp` @ v1.4.1 (the version pinned in `go.mod`)
- **Transport Subsystem**: Standard Input/Output (`stdio`) exclusively for parent process streaming.

## 3. Automation, Quality Assurance & Build Commands
- Run Formatting & Lint Compliance: `go fmt ./...`
- Synchronize & Lock Dependencies: `go mod tidy`
- Run the Test Automation Pipeline: `go test -v ./...`
- Local Binary Target Compilation: `go build -o bin/macos-darwin-mcp ./cmd/macos-darwin-mcp`

## 4. Fundamental Engineering Axioms (Non-Negotiable)
1. **Zero Stream Corruption (`os.Stdout`)**: The `os.Stdout` channel belongs strictly to the JSON-RPC messaging loop. All internal log engines (`log.Printf`), fmt print statements, process initialization output, and inner panicked stack traces MUST be routed explicitly to `os.Stderr`. Any loose string leakage to stdout will break protocol framing and crash the client interface.
2. **Defensive Parameter Slicing**: Bypassing tokenization via shell string mapping is banned. All native utilities must be invoked via `exec.CommandContext` using explicit positional string arrays (`[]string`). Shell wrappers (`sh`, `bash`, `zsh`, `eval`) are entirely forbidden.
   - **`osascript` / option-injection hardening (mandatory for every AppleScript-backed capability)**: passing argv to a tool via `exec.CommandContext` stops *shell* injection, but it does NOT stop *option* injection — a model-supplied value that begins with `-` can still be parsed by the target binary as one of ITS OWN flags. For `osascript` this is a code-execution hole: a value like `-e` is read as an extra `-e <statement>` and the next argument is executed as AppleScript, defeating the "data, never code" property. So any capability that shells out to `osascript` (or any other binary fed model-controlled values) MUST neutralize this before assembling argv: insert a `--` end-of-options terminator after the script source so every following value is treated strictly as the script's `on run argv` (osascript consumes the `--` and does not pass it into argv). Where a binary has no `--` terminator (e.g. `mdfind`), reject dash-leading values up front instead (see `.claude/rules/darwin-execution.md` §4). Every such capability MUST ship a regression test that feeds a flag-like value (e.g. subject `-e`) and asserts it lands as data, not as a flag — these are easy to break silently later.
3. **Transactional Execution Loop for Mutating State**: Tools that delete, overwrite, or mutate system configurations must map to a discrete two-phase architecture: `Stage` (validates intent, calculates risk indices, signs with an ephemeral Request ID) and `Commit` (executes the staged data structure upon explicit external confirmation payload).
4. **Code Quality**: Write modular, maintainable code by strictly following SOLID principles (Single responsibility, Open/closed, Liskov substitution, Interface segregation, Dependency inversion).
5. **Self-Documenting Code (Always)**: All code MUST be written so that a human or a review agent can understand it without reading the surrounding implementation. This is non-negotiable for every file and every change:
   - **File header section**: every source file opens with a package/file-level doc comment explaining what the file is responsible for and how it fits into the wider architecture.
   - **Function & type docstrings**: every exported (and every non-trivial unexported) function, method, type, and field carries a Go doc comment stating its purpose, contract, and any invariants or failure modes — not a restatement of the signature.
   - **Explanatory inline comments**: comment the *why* behind non-obvious logic, security guardrails, and design trade-offs; let clear naming carry the *what*.
   - The goal is a codebase that documents itself. Prefer clarity over cleverness; if a reader would need to ask "why is this here?", answer it in a comment.

## 5. Directory Structure & Context Delegation
Subdirectory rule files enforce specialized runtime constraints based on the active domain layout:
- Go Programming Conventions & SDK Types: `.claude/rules/go-conventions.md`
- Secure Darwin Subprocess Management: `.claude/rules/darwin-execution.md`
- State Staging & Defensive Operations: `.claude/rules/transactional-state.md`

### Code Layout (capability-engine architecture)
The server is built around a **capability registry + a fixed engine** (the design is recorded in `docs/ideas/macos-mcp-capability-engine.md` and specified in `docs/specs/capability-engine-readonly-spine.md`). Operations are described as **data** (JSON manifests), not hand-written per-operation tools, so a new operation is a manifest entry rather than new Go code.

- `cmd/macos-darwin-mcp/` — the entry point; wiring only (load registry → build engine/server → serve over stdio).
- `internal/registry/` — the capability catalog: types, the embedded JSON manifests under `manifests/`, and fail-fast structural validation. Pure data; no `os/exec`, no MCP.
- `internal/engine/` — execution: parameter validation/normalization, argv assembly (a declarative generic builder plus named builders for irregular grammars, and in-process "builtin" builders), and the subprocess runner.
- `internal/policy/` — the trust boundary deciding which binaries may run.
- `internal/server/` — the MCP adapter. The model sees a small, **fixed** set of domain tools (currently `filesystem`), then executes manifest-backed operations by calling that tool with `operation` + `params`. Dependency direction: `server → engine → policy`, with both depending on `registry` types and never the reverse.

### 6. Compile as a Universal 2 Binary

The first Mac laptops to ship with the Apple Silicon M1 chip (announced and released in November 2020) shipped with macOS 11.0 Big Sur

Under the hood, the corresponding Darwin kernel version that introduced native ARM64 support for Apple Silicon is **Darwin 20.1.0**.

To ensure your Go-based MCP server runs natively on both modern Apple Silicon chips (M1, M2, M3, M4) and older Intel-based Macs that are still running legacy macOS versions, you should compile your Go server into a **Universal 2 Binary**.

Go handles this through cross-compilation environment variables. You can add a dedicated release script or modify your `.claude/skills/verify-pipeline.md` automation file to compile both targets and stitch them together using macOS’s native `lipo` tool:

```bash
# 1. Compile the Apple Silicon (ARM64) slice
GOOS=darwin GOARCH=arm64 go build -o bin/mcp-server-arm64 ./cmd/macos-darwin-mcp

# 2. Compile the Intel (AMD64) slice
GOOS=darwin GOARCH=amd64 go build -o bin/mcp-server-intel ./cmd/macos-darwin-mcp

# 3. Stitch them together into a single Universal Binary
lipo -create -output bin/macos-darwin-mcp bin/mcp-server-arm64 bin/mcp-server-intel

# Clean up individual slices
rm bin/mcp-server-arm64 bin/mcp-server-intel

```

## 7. Plain-Language Reporting to the User

When explaining your reasoning, your design decisions, or what you just implemented, write in plain technical language that a senior engineer can understand WITHOUT reading the code you wrote. This governs chat/status explanations (it is distinct from Axiom 5, which governs in-code comments).

- Lead with what a component *does* and *why it matters*, not the names of its functions or fields. A reader should grasp the behavior before ever seeing an identifier.
- Avoid dense jargon stacks and compressed noun-chains (e.g. "ParamSpec-driven normalization with JSON-type coercion"). If a term is unavoidable, define it in one short clause the first time it appears.
- Prefer concrete examples ("turns `{all: true}` into the argument `-A`") over abstract descriptions.
- Identifiers and file paths are fine as *supporting* detail, but the explanation must stand on its own without them.
- Optimize for a busy human skimming for understanding, not for maximal information density.

## 8. Documentation Upkeep (Non-Negotiable)

1. Always update `README.md`, as necessary, after any new feature implementation.
2. Always update `docs/TESTS.md`, as necessary, whenever new test sets are written.
3. Save design choices or notes from an implementation as a new markdown file in `docs/issues/`, using:

   ```
   **note**
   <description>
   ```

4. Save issues (deferred scope, known limitations, flagged-for-awareness items — not defects) as a new markdown file in `docs/issues/`, using:

   ```
   **issue**
   <description>

   **fixed**
   <how, or leave blank/absent until resolved>
   ```

5. Save bugs (actual defects) as a new markdown file in `docs/issues/`, using:

   ```
   **bug**
   <description>

   **fixed**
   <how, or leave blank/absent until resolved>
   ```
