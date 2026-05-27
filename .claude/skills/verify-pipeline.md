---
name: verify-pipeline
description: Automates code formatting, lints imports, processes test execution, and ensures structural compilation consistency
---

# Automation Protocol: Verify Pipeline Execution

When this skill is triggered, execute the following operational validation steps in order:

1. **Syntax Formatting Pass**: Execute `go fmt ./...` across all workspace targets to align indentation structures cleanly.
2. **Dependency Tree Stabilization**: Execute `go mod tidy` to clean up old or unneeded requirements and lock the official `v1.6.0` Go MCP SDK mapping.
3. **Unit & Integration Regression Testing**: Run `go test -v ./...` to verify that transactional mapping tools and error parsing routines do not break core logic constraints.
4. **Standalone Native Compilation**: Run `go build -o bin/macos-darwin-mcp main.go` to confirm the compiler generates a pristine, zero-dependency executable target binary.

If any validation phase encounters an anomaly or compilation fault, halt the pipeline immediately, isolate the problematic line number, and propose a concise correction step to the developer.

