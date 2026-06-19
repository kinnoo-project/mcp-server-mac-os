# PR #7 review — Mvp2/impl4

2026-06-18, mode: fix

Copilot raised four comments, all about `osascript`/`mdfind` option (flag)
injection — the same "data, never code" property `.claude/rules/darwin-execution.md`
exists to protect. Three were correct as written; one had a valid concern but
an incorrect fix, addressed differently and explained below.

---

(internal/engine/mutate_mail.go) `osascript` is invoked without a `--`
end-of-options terminator, so a model-supplied value like subject="-e" could
be parsed as an additional osascript flag and the next argument executed as
AppleScript (option injection).

**fixed**
Confirmed the injection is real, not theoretical: `osascript -e <script> "-e"
"EVIL"` parses the second `-e` as another statement (reproduced a syntax
error). Inserted `--` between the AppleScript source and the first
model-supplied argument: `["-e", script, "--", subject, body, count,
recipients..., attachments...]`. Verified empirically that osascript consumes
the `--` itself and does NOT pass it into `on run argv`, so the script's item
indices are unchanged. Added an explanatory comment at the argv-assembly site.

---

(internal/engine/mutate_mail_test.go, line 50) send_mail argv-shape assertions
need to account for the `--` terminator.

(internal/engine/mutate_mail_test.go, line 111) Attachment argv-shape assertion
likewise.

**fixed**
Updated both assertions: expected length 7 → 8, and the subject/body/count/
recipient/attachment indices each shift up by one. Added an explicit
`args[2] == "--"` check, and a new regression test
`TestStageSendMail_FlagLikeSubjectStaysData` that stages subject="-e" with an
AppleScript-looking body and asserts the "-e" lands as data after the
terminator — the test that would have caught the original gap.

---

(internal/engine/builtins_mail.go) `mdfind` is called with the query as the
last argv element without a `--` terminator; a query starting with "-" may be
parsed as a flag. Suggested adding `--` before the query.

**fixed (differently — the suggested fix does not work)**
The concern is valid but `--` is NOT the remedy here: `mdfind` has no
end-of-options terminator and rejects `--` outright ("Unknown option --",
exit 1), so adding it would break every search. Applied the
`.claude/rules/darwin-execution.md` §4 guardrail instead — reject a query that
begins with "-" before mdfind runs, with a clear error explaining why. Only
the first character matters (mdfind treats the whole argument as the query once
it begins with a non-dash, so "foo -bar" is already safe). Documented the
restriction in the search_mail manifest param description and added a
regression test `TestRunSearchMail_RejectsDashLeadingQuery`.

Note: `mdls` (also in builtins_mail.go) is invoked with the matched file path
last, but those paths come from mdfind output and are always absolute
(`/Users/...`), so they cannot begin with "-"; left as-is.
