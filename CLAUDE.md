# Comprehensive macOS Darwin MCP Server - Unified Engineering Manual

## 1. Project Vision & Lifecycle Goal
This project provides a highly scalable, enterprise-grade Model Context Protocol (MCP) server that safely translates natural language intent into any native macOS (Darwin) CLI configuration pattern. The core goal is absolute system flexibility paired with structural, programmatic guardrails.

## 2. Technical Stack Context
- **Language Layer**: Go 1.26+ (Idiomatic, strictly typed concurrency structures)
- **Official SDK**: `github.com/modelcontextprotocol/go-sdk/mcp` @ v1.6.0
- **Transport Subsystem**: Standard Input/Output (`stdio`) exclusively for parent process streaming.

## 3. Automation, Quality Assurance & Build Commands
- Run Formatting & Lint Compliance: `go fmt ./...`
- Synchronize & Lock Dependencies: `go mod tidy`
- Run the Test Automation Pipeline: `go test -v ./...`
- Local Binary Target Compilation: `go build -o bin/macos-darwin-mcp main.go`

## 4. Fundamental Engineering Axioms (Non-Negotiable)
1. **Zero Stream Corruption (`os.Stdout`)**: The `os.Stdout` channel belongs strictly to the JSON-RPC messaging loop. All internal log engines (`log.Printf`), fmt print statements, process initialization output, and inner panicked stack traces MUST be routed explicitly to `os.Stderr`. Any loose string leakage to stdout will break protocol framing and crash the client interface.
2. **Defensive Parameter Slicing**: Bypassing tokenization via shell string mapping is banned. All native utilities must be invoked via `exec.CommandContext` using explicit positional string arrays (`[]string`). Shell wrappers (`sh`, `bash`, `zsh`, `eval`) are entirely forbidden.
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

### 6. Compile as a Universal 2 Binary

The first Mac laptops to ship with the Apple Silicon M1 chip (announced and released in November 2020) shipped with macOS 11.0 Big Sur

Under the hood, the corresponding Darwin kernel version that introduced native ARM64 support for Apple Silicon is **Darwin 20.1.0**.

To ensure your Go-based MCP server runs natively on both modern Apple Silicon chips (M1, M2, M3, M4) and older Intel-based Macs that are still running legacy macOS versions, you should compile your Go server into a **Universal 2 Binary**.

Go handles this through cross-compilation environment variables. You can add a dedicated release script or modify your `.claude/skills/verify-pipeline.md` automation file to compile both targets and stitch them together using macOS’s native `lipo` tool:

```bash
# 1. Compile the Apple Silicon (ARM64) slice
GOOS=darwin GOARCH=arm64 go build -o bin/mcp-server-arm64 main.go

# 2. Compile the Intel (AMD64) slice
GOOS=darwin GOARCH=amd64 go build -o bin/mcp-server-intel main.go

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

