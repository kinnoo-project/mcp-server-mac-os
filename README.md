# mcp-server-mac-os

A read-only MCP server for inspecting a macOS filesystem in natural language
through any MCP-aware client (Claude Code, Claude Desktop, etc.).

It exposes a deliberately small, **non-mutating** set of tools that wrap native
macOS utilities. The server cannot create, modify, move, or delete files — only
inspect them.

| Tool   | Wraps         | Use it for                                                    |
| ------ | ------------- | ------------------------------------------------------------- |
| `ls`   | `/bin/ls`     | "What's in my Downloads folder?"                              |
| `pwd`  | (Go stdlib)   | "Where is the server running from?"                           |
| `file` | `/usr/bin/file` | "What kind of file is this?"                                |
| `grep` | `/usr/bin/grep` | "Which files mention `TODO`?"                               |
| `du`   | `/usr/bin/du` | "How big is this folder?"                                     |
| `find` | `/usr/bin/find` | "List all PNG and JPG files under `~/Pictures`."            |
| `stat` | `/usr/bin/stat` | "When was this file last modified?"                         |
| `wc`   | `/usr/bin/wc` | "How many lines are in this log?"                             |

## Why this server is safe to expose

- **No shell, ever.** Every utility is invoked with `exec.CommandContext` and a
  pre-tokenized `[]string`. There is no `sh -c`, no string concatenation, no
  glob expansion done by us.
- **Trusted binaries only.** Each binary is resolved with `exec.LookPath` and
  then verified to live under `/bin`, `/sbin`, `/usr/bin`, or `/usr/sbin`.
- **Read-only command surface.** Mutating commands (`rm`, `mv`, `cp`, `mkdir`,
  `chmod`, `dd`, `tee`, `ln`, …) are not registered. `find` is exposed without
  `-exec`, `-delete`, or `-prune`.
- **Argument validation.** `find`'s `extensions` filter rejects any value that
  isn't `[A-Za-z0-9_-]+`; `find`'s `type` is restricted to `f`, `d`, or `l`.
- **Output budget.** Any tool output larger than 8 KB is compacted to a head
  + tail window with a structural notice so the LLM context isn't saturated.
- **Stdout discipline.** All logs go to `os.Stderr`; `os.Stdout` is reserved
  for JSON-RPC framing.
- **macOS permission model.** The server runs as the user that started it, so
  it inherits the same Full-Disk-Access, Files-and-Folders, and standard POSIX
  permissions. Permission denials surface verbatim from the underlying utility
  (look for `[stderr]` in tool output).

## Build

The project targets Go 1.26+ (matching `go.mod`) and the official MCP Go SDK.

```bash
go mod tidy
go build -o bin/macos-darwin-mcp ./src
```

For a Universal 2 binary that runs on both Apple Silicon and Intel Macs:

```bash
GOOS=darwin GOARCH=arm64  go build -o bin/mcp-server-arm64 ./src
GOOS=darwin GOARCH=amd64  go build -o bin/mcp-server-intel ./src
lipo -create -output bin/macos-darwin-mcp bin/mcp-server-arm64 bin/mcp-server-intel
rm bin/mcp-server-arm64 bin/mcp-server-intel
```

## Add to Claude Code (`claude mcp add`)

After building, register the binary with Claude Code:

```bash
claude mcp add mac-os-fs -- /absolute/path/to/bin/macos-darwin-mcp
```

That registers a `stdio` MCP server named `mac-os-fs`. Verify it:

```bash
claude mcp list
```

You should see `mac-os-fs` connected, exposing the eight tools above. Restart
your Claude Code session if a tool list appears stale.

### Add to Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "mac-os-fs": {
      "command": "/absolute/path/to/bin/macos-darwin-mcp"
    }
  }
}
```

Then quit and relaunch Claude Desktop.

## Try it in natural language

Once the server is registered, prompts like the following will be answered by
Claude calling the right combination of tools:

- *"How many files are in my Downloads folder, and what is the total file
  size?"* → `find ~/Downloads -type f` + `du -sh ~/Downloads`.
- *"List all image files (PNG, JPG, HEIC, GIF) under ~/Pictures and the total
  size."* → `find` with `extensions=["png","jpg","jpeg","heic","gif"]` + `du`.
- *"Which files in this repo contain the string `TODO`?"* → `grep -rn TODO .`.
- *"What kind of file is `~/Downloads/mystery.bin`?"* → `file`.
- *"How many lines are in `/var/log/system.log`?"* → `wc -l`.

The first time Claude reaches into a protected location (Desktop, Documents,
Downloads, iCloud Drive, external volumes, etc.), macOS will prompt for
permission for the **terminal/host process that launched Claude**. Granting
once is enough; subsequent calls reuse the grant.

## Quick handshake smoke test

Without a Claude client, you can drive the server directly over stdio:

```bash
( printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
    '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ls","arguments":{"path":"~"}}}' ;
  sleep 0.3
) | ./bin/macos-darwin-mcp
```

You should see an `initialize` response, a `tools/list` response naming the
eight tools, and an `ls` of your home directory.

## Develop & test

```bash
go fmt ./...
go vet ./...
go test -v ./...
```

The test suite exercises every handler against a temporary directory tree and
asserts that the registered tool surface stays read-only — see
`src/tools_test.go`.

## Project layout

```
src/
  main.go        # stdio server boot + tool registration
  tools.go       # MCP tool definitions and handlers
  exec.go        # subprocess helpers (binary resolution, ~ expansion, output compaction)
  tools_test.go  # handler tests + read-only invariant guard
```

For the architectural ground rules behind these choices, see `CLAUDE.md` and
`.claude/rules/*.md`.
