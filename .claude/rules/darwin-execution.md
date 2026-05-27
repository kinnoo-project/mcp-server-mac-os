---
name: darwin-process-security
description: Regulates system utility orchestration, tokenized argument configuration, and environment hygiene
applyTo: ".*\\.go"
---

# Secure Darwin Native Subprocess Routines

## 1. Absolute Isolation from Shell Expansion
You are completely forbidden from writing code that passes a single string expression block to the terminal kernel. All inputs must be bound into an explicit positional array format.

```go
// ❌ CRITICAL SEVERITY FAILURE: High vulnerability to path modification or string chaining
cmd := exec.CommandContext(ctx, "sh", "-c", "my-command " + userText)

// ✅ DEFENSIVE ARCHITECTURE STANDARD: OS evaluates inputs strictly as bounded parameter data
cmd := exec.CommandContext(ctx, "/usr/bin/native-tool", "flag", userText)

```

## 2. Output Stream Payload Compaction

Verbose native system utilities can return massive data outputs that saturate an LLM's context window. Implement custom truncation logic inside your stream extraction loop to enforce a maximum token safety threshold:

* If standard output reads exceed 8,000 bytes, trim the text array.
* Retain the initial 4,000 bytes and trailing 4,000 bytes cleanly.
* Inject an explicit structural message indicating exactly how many lines were dropped during compilation processing.

## 3. Explicit Binary Suffix & Verification Checks

When invoking native utilities, look up their exact target execution footprint using `exec.LookPath` or enforce the absolute root path layout mapping directly to Darwin system boundaries (e.g., `/usr/bin/`, `/usr/sbin/`, `/sbin/`). This blocks rogue binary substitutions in local workspaces.

```
