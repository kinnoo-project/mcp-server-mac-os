---
name: transactional-state-management
description: Architecturally guides how agents manage high-risk actions, staged state persistence, and file fallbacks
applyTo: ".*\\.go"
---

# State Transaction & Fallback Control Specifications

## 1. The Two-Phase Staging Registry Pattern
Because MCP tools operate on standard stateless loops, any high-risk system modification requested by the LLM must register state inside an internal thread-safe transactional queue.

- **Phase 1: `StageAction`**: Captures proposed system execution matrices, validates parameters, maps a secure random tracking signature string (`req_...`), assigns a risk score index, and persists the payload into memory. It returns this signature token to the caller.
- **Phase 2: `CommitAction`**: Requires the caller to supply the valid tracking token. It extracts the matching structural data parameters from memory and fires the underlying Darwin command.

## 2. Memory Lock Isolation
Your global transaction state maps must protect memory states from read/write corruption during high-frequency parallel routines. Use native synchronization structures:

```go
var (
	transactionRegistry = make(map[string]PendingTransaction)
	registryLock        sync.RWMutex
)

```

## 3. Native Fallback Mechanism (Recycling Rules)

When writing tools designed to delete or overwrite system configurations or files, never call final destructive purging actions directly.

* Implement local structural fallbacks.
* For files, route target mutation structures through macOS User Trash APIs (`~/.Trash/`) or construct standard staging directory targets inside `/tmp/mcp-fallback/` before executing state upgrades. This gives the local user an immediate path to manual state restoration.

```
