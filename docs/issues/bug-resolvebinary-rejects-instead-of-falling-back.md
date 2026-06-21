**bug**
`policy.ResolveBinary` failed to locate a genuine system binary whenever `$PATH`
contained a same-named executable outside the trusted directories EARLIER in the
search order. It called `exec.LookPath` first and then only *re-checked* that
single hit against the trusted directories — if the hit was untrusted it returned
an error instead of falling back to scanning `/bin`, `/sbin`, `/usr/bin`,
`/usr/sbin` for the real tool.

Impact: the server (and its tests) broke on any machine whose `$PATH`
front-loaded a foreign toolchain. It surfaced in CI on `macos-latest`, where an
Android SDK `sqlite3` (`/Users/runner/Library/Android/sdk/platform-tools/sqlite3`)
preceded `/usr/bin`, so `check_messages`/`search_messages`/`read_conversation`
and the new security tests all failed with "resolved binary ... is outside
trusted Darwin directories". A real user with, e.g., Homebrew or an SDK ahead of
`/usr/bin` would have hit the same failure for the affected capabilities.

The rogue binary was never executed (the rejection was safe), but the fallback to
the legitimate system tool was missing.

**fixed**
`ResolveBinary` now treats an untrusted `LookPath` hit as "ignore and continue"
rather than an error: it accepts a PATH hit only when that hit is itself under a
trusted directory, and otherwise falls through to scanning the trusted
directories directly for the genuine binary. This preserves the anti-rogue-
substitution guarantee (the untrusted hit is never returned) while restoring
functionality under an unusual `$PATH`. Regression test:
`TestResolveBinary_IgnoresRogueOnPath` in `internal/policy/binaries_test.go`
plants a rogue `ls` ahead on `$PATH` and asserts resolution returns the trusted
system `ls`, never the plant.
