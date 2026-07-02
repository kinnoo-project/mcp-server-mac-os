---
name: runevals
description: Run the MCP server's eval suite in-session — Claude directly calls the MCP tools based on each case's prompt and checks expectations. No external Anthropic API calls, no API credits spent. Use instead of `go run ./cmd/runevals` for day-to-day development.
---

# runevals — in-session eval runner

Triggered as `/runevals` (optionally with flags parsed from the argument string:
`-only <id>`, `-include-manual`, `-dry-run`).

Runs the eval cases from `evals/cases/` within the **current Claude session**. No
external Anthropic API calls; Claude itself acts as the model being evaluated —
it reads each prompt and calls the appropriate MCP tool(s), then checks the
result against the case's `expect` fields. Free, immediate, verbose by default.

## When to use vs. the binary

| Situation | Use |
|---|---|
| Day-to-day: check tool routing + execution | `/runevals` (this skill) |
| CI / unattended / strict selection testing | `go run ./cmd/runevals` |

The binary tests whether a fresh model (with no session context) routes correctly.
In-session, Claude has conversation history, so selection tests are softer. The
real value here is execution correctness — the tool is called and the result (state,
errors, text) is verified for free.

## Step 1 — Parse arguments

From the skill argument string, extract:
- `-only <id>` — run just this one case by ID
- `-include-manual` — include cases with `"manual": true`
- `-dry-run` — list cases and stop; make no tool calls

## Step 2 — Load cases

Read every `*.json` file under `evals/cases/` using the Read tool. Each file is a
JSON array of case objects. Collect all cases into one flat list. The case format:

```json
{
  "id": "case_id",
  "manual": false,          // optional; skip unless -include-manual
  "setup": {                // optional; create temp fixtures before any turn
    "scratch": "subdir",    // name for the temp dir (single path segment)
    "files": ["a.txt", "subdir/b.txt"]  // empty files to create inside scratch
  },
  "teardown": {
    "remove_scratch": true  // delete scratch dir after all turns
  },
  "turns": [
    {
      "prompt": "...",
      "expect": {
        "tool": "filesystem",             // MCP tool name (optional)
        "operation": "move",              // operation arg value (optional)
        "forbid_tools": ["pipeline"],     // must NOT call these (optional)
        "text_contains": "moved",         // response text must contain this (optional)
        "tool_succeeds": true,            // tool must not return an error (optional)
        "state": {                        // filesystem post-conditions (optional)
          "exists": ["/path/to/file"],
          "absent": ["/path/that/should/be/gone"],
          "is_dir": ["/path/that/should/be/a/dir"]
        }
      }
    }
  ]
}
```

## Step 3 — Dry run (if -dry-run)

List each case as:
```
  - <id> (<N> turn(s)) [manual]
```
annotating `[manual]` for manual-tagged cases. Then print `dry run OK; no tool calls were made.` and stop.

## Step 4 — Run cases

For each case (filtering by `-only`, skipping `manual: true` unless
`-include-manual`):

### 4a. Print RUN line
```
RUN   <id>
```

### 4b. Apply setup (if case has `setup`)

Generate a short random token (use the current Unix timestamp + case index as a
stand-in for `{{unique}}`). Create the scratch directory:
```bash
SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/mcp-eval-XXXXXX")
```
Then create each file listed in `setup.files` inside the scratch dir using Bash:
```bash
touch "$SCRATCH/filename"
mkdir -p "$SCRATCH/subdir" && touch "$SCRATCH/subdir/file"
```
Record `scratch=$SCRATCH` and `unique=<token>` for substitution in prompts and
state paths.

### 4c. For each turn

**Substitute placeholders** in the turn's prompt and all state paths:
- `{{unique}}` → the random token
- `{{scratch}}` → the resolved scratch directory path

**Call the appropriate MCP tool.** Read the prompt and decide which tool and
operation to call — **base this on the prompt's semantic intent, not on the
`expect` field**. Set the expected answer aside mentally while deciding; this
tests your actual routing. Then call the tool with the appropriate parameters
using your MCP tool access.

Record which tool(s) you called and what operation(s) you used, plus the full
response text and whether the response was an error.

**Check expectations:**

| Field | Check |
|---|---|
| `tool` | The first tool you called must match this name exactly |
| `operation` | The `operation` arg you passed must match this value |
| `forbid_tools` | You must NOT have called any tool in this list |
| `text_contains` | The tool's response text must contain this string (case-insensitive) |
| `tool_succeeds: true` | `tool` must be set; the tool must not have returned an error result |
| `state.exists` | Each path must exist — verify with `Bash: test -e "<path>" && echo ok` |
| `state.absent` | Each path must NOT exist — verify with `Bash: ! test -e "<path>" && echo ok` |
| `state.is_dir` | Each path must be a directory — verify with `Bash: test -d "<path>" && echo ok` |

If any check fails, record the failure reason and stop this case's remaining
turns. Do not apply teardown — leave the scratch dir for inspection.

### 4d. Apply teardown (if all turns passed and `teardown.remove_scratch` is true)

```bash
rm -rf "$SCRATCH"
```

### 4e. Print result
```
PASS  <id>
```
or
```
FAIL  <id>: turn N: <reason>
```

## Step 5 — Print summary

```
N/M passed
```
Return exit-1 equivalent (surface to user) if any cases failed.

## Honest limits

- **Selection accuracy is softer in-session**: Claude can see the `expect` field
  while processing. Be disciplined: decide which tool to call from the prompt
  alone, then check. The binary runner tests selection with a truly fresh model.
- **Manual cases**: many need real permissions (Full Disk Access, Contacts, etc.)
  or signed-in accounts. Running them in-session requires those grants in this
  terminal session. Check before running with `-include-manual`.
- **Mutation turns**: cases with stage→execute turns call real system operations.
  Self-cleaning teardown turns are part of the case design — they run as long as
  all earlier turns pass.
- **Real time cost**: each tool call is synchronous in-session but costs no
  Anthropic API tokens. Network-dependent tools (ping, DNS) add wall-clock time.
