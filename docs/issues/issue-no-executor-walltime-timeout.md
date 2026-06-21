**issue**
The shared subprocess path in `internal/engine/executor.go` (`execCommand`,
behind `runCommand`/`RunCommand`) binds each child process only to the incoming
request context. It enforces no independent wall-clock timeout. A capability
whose underlying tool can run for a very long time over a large input — for
example `find` or `du` rooted at `/`, a recursive `grep` over a huge tree, or a
`cp -R` of an enormous directory — is therefore bounded only by whenever the MCP
client happens to cancel the request. A caller (or a prompt-injected model) can
use this to tie up CPU/IO for an extended period: a time/resource denial of
service.

The network builtins already self-bound (see `scanLanTimeout` and the per-probe
timeouts in `builtins_network.go`); the gap is that the generic path does not.

This was identified while building the security test suite. It was deliberately
left out of that suite's scope (which adds tests, not new guardrails) and
deferred to a follow-up hardening PR. The security suite includes a skipped
placeholder, `TestBounds_ExecutorWallClockTimeout` in
`internal/engine/bounds_test.go`, naming where the regression coverage will live
once the fix lands.

Suggested fix: derive a bounded context (e.g. `context.WithTimeout`) from the
request context inside `execCommand`, with a generous default ceiling and
possibly a per-capability override, so a runaway read is terminated even if the
client never cancels.

**fixed**
