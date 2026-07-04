**bug**
`application-contacts.get_contact` and `application-music.now_playing` are 100% non-functional. Both declare a bare AppleScript variable named `st`:

- `internal/engine/builtins_contacts.go:60-61` (the `_addr` handler, used by `get_contact`)
- `internal/engine/builtins_music.go:76-77,83` (`nowPlayingScript`, used by `now_playing`)

`st` is a reserved AppleScript unit-abbreviation token (the weight unit "stone"), so `set st to ...` always fails to compile — a language-level fact true on any Mac, not a machine quirk. Confirmed in isolation: a bare `osascript -e 'set st to "hello"'` fails with `Expected expression but found "st". (-2741)`; the identical script with `st2` or `state` in place of `st` compiles and runs fine. No third-party scripting additions are involved (`/Library/ScriptingAdditions` and `~/Library/ScriptingAdditions` are both empty on the test machine).

Found during an eval run on 2026-07-03 (see `evals/outputs/eval-run-20260703-182847.md`) — the routing-only eval cases (`get_contact_routing`, `now_playing_routing`) pass because they only assert `tool`/`operation`, not `tool_succeeds`, so the failure was silent until the actual response text was inspected.

**fixed**
Renamed `st` → `stVal` in `builtins_contacts.go`'s `_addr` handler and `st` → `stateVal` in `builtins_music.go`'s `nowPlayingScript` (every read-back site updated in the same handler). Verified live against the rebuilt binary: `get_contact` and `now_playing` now compile and run rather than failing with the AppleScript syntax error.

Also added `internal/engine/applescript_compile_test.go` (`TestAllEmbeddedAppleScriptsCompile`), a registry-driven test in the same spirit as `injection_sweep_test.go`: it enumerates all ~70 top-level AppleScript source constants in the package and compiles each with `osacompile -e <script> -o <tmp>` — which never executes the script (no `tell application` block ever runs, so no Apple Event is sent and no Automation grant is needed) but does catch any syntax error, including a bare reserved-word collision like this one. All 70 pass after the rename; this test would have failed on `getContactAddrHelper`/`nowPlayingScript` before the fix.
