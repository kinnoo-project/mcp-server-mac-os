**issue**

`send_message` sends an iMessage through Messages.app's AppleScript dictionary
(`sendIMessageScript` in `internal/engine/mutate_messages.go`). Messages'
scripting `send` has been historically reliable but has varied across macOS
releases — the `service` / `buddy` terminology and whether a plain handle
resolves to a sendable buddy have shifted between versions, and Apple has not
treated Messages automation as a priority. This makes it the one genuinely
version-sensitive piece of the Messages domain.

It is NOT exercised by automated tests: like `send_mail`, sending is irreversible
(no unsend), so no test executes the staged `Forward` command — tests cover only
argv construction (including the `--` option-injection terminator), recipient
validation, and the verbatim preview. The send path is therefore verified by
**manual smoke test**: stage a `send_message` (which changes nothing and returns
a preview + token), then `execute` it to your **own** number/Apple ID and
confirm the message arrives.

If `send` fails on a given macOS version, the fix is localized to the fixed
`sendIMessageScript` constant (e.g. adjusting `service`/`buddy`/`participant`
terminology); the surrounding hardened-osascript plumbing and the
stage→execute gate are unaffected.

**fixed**

Not a defect in this code — recorded as a known macOS-version sensitivity and
surfaced via the manual-smoke-test guidance in docs/TESTS.md and the README.
