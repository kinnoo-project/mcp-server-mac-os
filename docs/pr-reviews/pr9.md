# PR #9 review — Add application-phone capability: find_contact + call

2026-06-19, mode: fix

Two Copilot comments, both correct and addressed.

---

(internal/engine/mutate_phone.go) `resolveSingleNumber` can auto-select a single
matching contact number even when it fails `canonicalizePhoneNumber`,
contradicting the "cannot be auto-selected" comment and producing a less
actionable error later.

**fixed**
Split the decision logic out of the live Contacts query into a pure
`chooseContactNumber(name, contacts)`. It now auto-selects only when the single
distinct candidate actually canonicalizes; a lone but un-dialable number falls
through to a clear, specific error ("the only number found for X … is not in a
dialable format; re-issue with an explicit 'number'") instead of being returned
and failing canonicalization in the caller. Added `TestChooseContactNumber`
covering: no match, single dialable, same number under two labels (one
distinct), single un-dialable (the regression), and two-distinct ambiguity.

---

(internal/engine/builtins_phone.go) `find_contact` retrieved *all* matching
people from Contacts and only applied `limit` during rendering, unlike the
Calendar/Reminders reads which cap inside AppleScript — a broad query ("a")
could produce huge stdout and flood `call`'s `contact_name` ambiguity list.

**fixed**
`findContactScript` now takes a max-people argument and stops after that many
people inside the `tell` block, exactly like `query_events`' `maxN`, so the
subprocess output is bounded before it reaches Go. `resolveContactNumbers` takes
a `maxPeople` parameter: `find_contact` passes the (capped) user `limit`, and
`call`'s resolution passes `maxFindContactLimit`, which is more than enough to
detect ambiguity while keeping the candidate list bounded. (Also extracted a
small `labelOrPhone` helper to de-duplicate the label-defaulting that the
renderer and the ambiguity list both use.)

Note on testing: the AppleScript cap and the live Contacts query are not
unit-tested (no subprocess in tests, same posture as the other app domains); the
cap mirrors the already-reviewed `query_events` pattern, and the resolution
policy it feeds is now covered by `TestChooseContactNumber`.
