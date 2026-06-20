# Regression tests currently run

Run via `go test ./...` (plus `-race` on the store), organized bottom-up through
the architecture:

| Layer | What's actually being checked |
|---|---|
| **`internal/registry`** | Manifests parse and load; structural validation rejects malformed capabilities (duplicate names, unknown enum/type values, a flag-kind param missing its flag token); the new `TestRiskClassificationInvariant` checks every mutating capability carries non-`none` risk; `TestNew_Rejects`/`TestNew_AcceptsAutoCommit` cover the `auto_commit` rule (rejected on a read-only or medium/high-risk capability, accepted on a reversible low-risk one). |
| **`internal/policy`** | Binary resolution only ever returns a path under `/bin`, `/sbin`, `/usr/bin`, `/usr/sbin`; path-separator injection and rogue-substitution attempts are rejected. |
| **`internal/engine`** | Per-type parameter coercion (tilde expansion, enum/required checks, unknown-key rejection); the generic builder's flag → `--` → positional ordering; `find`/`grep`'s irregular named-builder grammars; `largest_files`' ranking. For mutation: `stageMkdir`'s forward/inverse/preview values, its existing-path and dash-leading-path guardrails, and a real stage → run-forward → run-inverse round trip against a temp directory; `stageWriteSetting`'s forward/inverse/preview values for both the unset-key case and the prior-value-capture case, its refusal to stage when the existing value isn't a plain boolean, its refusal of a setting name absent from the allowlist, a data sanity check that every curated entry has non-empty domain/key/label, and a real stage → run-forward → run-inverse round trip via the real `defaults` binary against a **synthetic allowlist entry pointing at a disposable temp file** (never a real curated domain — see Safety note below); and `stageSendMail`'s validation (rejects no recipients, an address with no `@`, an empty subject, a missing or directory attachment path), its count-prefixed argv layout (`["-e", script, "--", subject, body, recipientCount, recipients..., attachments...]` — the `--` is the osascript end-of-options terminator that blocks option injection, and the count is what lets two variable-length lists share one flat argv with no delimiter), its verbatim preview text (including the attachment-filename line, present only when attachments exist), and that `Inverse` is always `nil` (irreversible) — **no test ever executes the `Forward` command**, since that would send a real email (see Safety note below). For pipelines (`pipeline_test.go`): `SupportsPipeline`'s eligibility rule against the real manifest (read-only argv-builder capabilities eligible; builtins and mutators rejected); a real two-stage `find`→`wc` round trip; `MaxPipelineStages` enforcement; the first-stage-missing-input guard (refuses rather than hangs); a failing middle stage aborting with its exit code; and the intermediate-size cap, split into `TestRunPipeline_IntermediateSizeCapEnforced` (a non-final stage exceeding the lowered cap aborts) and `TestRunPipeline_FinalStageNotSizeCapped` (the cap does NOT apply to a pipeline's last/only stage — its raw output goes straight to the same uncapped compaction path `Run` uses). `TestRun_AcceptsStdinCapabilityRefusesStandaloneWithoutInput` (generic-builder, `wc`) and `TestRun_GrepRefusesStandaloneWithoutPaths` (named-builder counterpart — a PR review caught that `grep`'s `paths` needed `arg.kind: "positional"` for this guard to find it at all) cover the standalone-hang guard; `TestValidateBuilders_AcceptsStdin` covers its boot-time precondition (only read-only, argv-builder capabilities may set `accepts_stdin`). For `search_mail` (`builtins_mail_test.go`): `parseMdlsOutput`'s pure parsing against canned `mdls`-format text (including the `(null)`-missing-attribute case and that attribute order isn't assumed), `splitNonEmptyLines`, the required-`query` guard, the dash-leading-query injection guard (`mdfind` has no `--`), and a real `mdfind` call with a query engineered to match nothing (see Safety note below). For the shared osascript seam (`applescript_test.go`): `osascriptCommand` always inserts the `--` terminator before data, `parseDate`/`parseClock` reject malformed/impossible dates and times, `parseFmtDateTime`↔`fmtYMDHM`↔`ymdhm.args()` round-trip the probe format, and `asRows` splitting. For Calendar mutators (`mutate_calendar_test.go`): `stageAddEvent`'s full forward/inverse argv layout (`["-e", script, "--", calendar, title, location, notes, start…, end…]`), its `--`-terminator placement and a flag-like-title (`-e`) regression, its natural-key inverse, preview text, and validation rejections (missing calendar/title, bad date/time, end ≤ start); plus the pre-probe rejection paths of `stageModifyEvent`/`stageDeleteEvent`. For Reminders mutators (`mutate_reminders_test.go`): `stageAddReminder` across all three due shapes (timed/all-day/none), the always-incomplete-on-create flag, natural-key inverse, preview, and rejections (missing list/title, `due_time` without `due_date`, bad date); plus pre-probe rejections of modify/complete/delete. **No Calendar/Reminders test ever executes a `StagedPlan` command** (see Safety note below). For the phone domain (`mutate_phone_test.go`, `builtins_phone_test.go`): `canonicalizePhoneNumber`'s accept/reject table (formatted numbers canonicalize; empty, scheme-like `tel:`/`file://`/`http://` values, letters, an interior `+`, and out-of-range digit counts are rejected — the URL-scheme injection guard); `stageCall`'s `open tel:`/`facetime:`/`facetime-audio:` URL per method, `Inverse == nil` (irreversible), preview, the exactly-one-of `number`/`contact_name` guard, and bad-number rejection; and `find_contact`'s pure `parseContactRows`/`renderContacts`/`friendlyLabel` logic (grouping by person, `limit` capping people not rows, the `_$!<Mobile>!$_`→`Mobile` cleanup) plus the required-`name` guard. The `contact_name` resolution and Contacts lookup run a live `osascript`, so are not executed in tests — and **no test ever places a real call** (see Safety note below). `chooseContactNumber` (the pure contact-resolution policy split out of the live Contacts query) is covered directly: no-match, single dialable, same number under two labels, a lone un-dialable number (errors rather than being auto-selected), and two-distinct ambiguity. For the messages domain (`builtins_messages_test.go`, `mutate_messages_test.go`): `escapeSQLLiteral` (doubles quotes — the `' OR 1=1; DROP TABLE message;--` regression stays a literal — and rejects NUL), `plausibleEmail`, `handleMatchClause` (email exact-equality vs phone digit-suffix `LIKE`, too-short/invalid rejected), `messageRow` JSON decoding, `cappedLimit`, `previewText`/`renderMessages`, `resolveMessageRecipient`'s exactly-one-of guard on the non-Contacts path, and the `attributedBody` text recovery (`extractTypedstreamText`/`messageText`: round-trips text/emoji and the 0x81 two-byte-length path through a synthetic typedstream blob, prefers a present `text` column, and degrades a missing-marker/garbage-hex blob to "" rather than panicking — the layout was cross-checked against a real chat.db row); plus `stageSendMessage`'s `osascript` argv (`--` terminator + canonicalized handle/text + the count-prefixed attachment paths `[handle, text, attachmentCount, paths...]`, including a flag-like-text `-e` regression and a with-attachments and attachments-only-no-text case), `Inverse == nil`, verbatim preview (naming each attachment), and validation rejections (no text *and* no attachments, no/both recipients, bad phone/email handle, a missing attachment file, a directory attachment path). The live `sqlite3`/Contacts paths are not executed and **no test reads a real chat.db or sends a message** (see Safety note below). For the application domain (`builtins_apps_test.go`, `mutate_apps_test.go`): `renderAppList`'s pure de-dup/filter/sort/truncate logic against synthetic `mdfind` output, `validateAppName` (rejects empty/leading-dash/control-char names), and `stageFocusApplication`/`stageQuitApplication`/`stageOpenApplication` command construction — the app name lands as data after the osascript `--` terminator, a flag-like name (`-e`) is rejected (the option-injection regression), focus/quit are `Inverse == nil`; `open_application`'s live System-Events probe is not exercised (only its pre-probe validation). For the printer domain (`builtins_printers_test.go`, `mutate_printers_test.go`): `renderPrinterList`'s parsing of `lpstat -p -d` (idle + default marker, the disabled-queue hand-off note, no-printers), `lpArgs` ordering (`-d`/`-n`/`--`/file), `validateCopies`/`validatePrinterName` bounds, `stagePrintFile`'s `lp` argv + missing-file rejection + `Inverse == nil`, and `stagePrintTestPage` writing the embedded page to the `/tmp/mcp-fallback` scratch file then staging `lp -- <scratch>` — **no test ever runs a print** (`Forward` is never executed). For the system domain (`builtins_system_test.go`, `mutate_system_test.go`): `parseWifiDevice` (resolves the Wi-Fi interface from hardware-port output), `renderBluetoothStatus` (on/off + connected device names from `system_profiler -json`), `renderPowerStatus` (battery summary pass-through + Low Power Mode on/off parse), and `stageOpenSettings`'s pane→`x-apple.systempreferences:` URL mapping (every enum pane resolves, unknown pane rejected, `Inverse == nil`) — **no test ever opens a Settings window**. |
| **`internal/transaction`** | The token store's contract: round-trip, prefix/uniqueness, one-shot consumption, TTL expiry, opportunistic purging of expired entries on `Put` (so an abandoned-token workload can't grow the store without bound), and (under `-race`) safety under concurrent `Put`/`Take`. |
| **`internal/server`** | Behavioral tests drive every capability through the real domain-tool handler against a hermetic fixture tree; the in-process integration test drives the *actual* MCP protocol: tool listing across all ten domain tools (`filesystem`, `preferences`, `application`, `printer`, `system`, `application-mail`, `application-calendar`, `application-reminders`, `application-phone`, `application-messages` — each asserted to embed its full operation menu) plus `execute`/`undo`/`pipeline` (13 tools total), the full `mkdir` stage→execute→undo round trip, a **stage-only** `write_setting` call against a real curated setting (asserting a token+preview come back, deliberately never calling `execute` — see Safety note below), a real `find`→`wc` pipeline round trip over the protocol, a real `search_mail` no-match call, a **stage-only** `send_mail` call (asserting the irreversibility warning appears in the preview, never calling `execute` — see Safety note below), structured errors for bad operations/tokens/pipeline stages (including a mutator or an unknown capability name as a stage), the auto-commit lane (`TestDomain_AutoCommitRunsImmediately`: a low-risk `auto_commit` mutation built on the real `mkdir` mutator runs immediately — no `req_` staging — creates the directory, returns an `undo_` token, and that token reverses it), and two drift checks (`TestDefaultsAllowlist_MatchesManifestEnum` for the `setting` enum vs the engine's `defaultsAllowlist`; `TestSettingsPanes_MatchManifestEnum` for the `open_settings` `pane` enum vs the engine's `settingsPaneURLs` map). |

### Safety note: `send_mail` tests never send a real email

`send_mail` is irreversible, so unlike every other mutator there is no
disposable/synthetic target to test against — the only safe boundary is
**never executing `StagedPlan.Forward`** at all. Every test (engine-level
`mutate_mail_test.go` and the protocol-level integration test) inspects only
the staged plan's fields; none calls `RunCommand`, `execute`, or otherwise
runs the underlying `osascript` command.

### Safety note: Calendar/Reminders tests never touch real events or reminders

The `application-calendar` and `application-reminders` mutators are reversible,
but staging the modify/complete/delete operations runs a live AppleScript
*probe* that needs a real event/uid and a granted Automation permission, and
executing any forward/inverse command would change the developer's real
Calendar/Reminders. So — exactly like `send_mail` — **no test executes a
`StagedPlan` command**, and the probe-dependent operations are tested only on
their pre-probe validation paths. The genuinely new hand-written AppleScript
(the shared `_mkdate`/`_fmt`/`_clean`/`_str` helpers) was validated by running
it through `osascript` in isolation, with no `tell application` block, so it
needed no app access and triggered no permission prompt. See
`docs/issues/note-calendar-reminders-write-paths-not-executed-in-tests.md` for
how to smoke-test the write paths manually (safe by construction, via
stage → execute → undo).

### Safety note: messages tests never read real chat.db or send a message

The `application-messages` reads query the real Messages database and need Full
Disk Access; `send_message` is irreversible. So **no test opens chat.db or
executes a send** — tests cover the injection-safe SQL construction, JSON
decoding, validation, rendering, and the staged `send_message` plan's fields
only — including the optional `attachments` path(s): the count-prefixed argv
layout (`["-e", script, "--", handle, text, attachmentCount, paths...]`), the
text-or-attachment requirement, an attachments-only (no text) send, the
missing-file and directory-path rejections, and the attachment-naming preview.
Manual smoke-test: grant Full Disk Access, run `check_messages` to confirm
the read path and the friendly permission error, then `send_message` *stage* →
`execute` to your **own** number/Apple ID (try it with an `attachments` image
path, too). See
`docs/issues/note-imessage-applescript-send.md` and `docs/issues/note-messages-read-fda.md`.

### Safety note: phone tests never place a real call

`call` is irreversible (there is no "un-call"), so — like `send_mail` — **no
test executes a `StagedPlan`'s forward command**; tests inspect only the staged
`open` URL and preview. The `contact_name` path additionally runs a live
Contacts `osascript` probe (needing real contacts and a granted Automation
permission), so it is left to manual smoke-testing; only the `number` path and
the pre-resolution validation (exactly-one-of, number canonicalization) are
unit-tested. Manual smoke-test: `find_contact` to confirm lookup + the TCC
grant, then `call` *stage* to confirm the URL/preview, executing only against
your own number.

### Safety note: `search_mail` tests never touch real mail content

The one real `mdfind` call in the test suite (`builtins_mail_test.go` and the
protocol-level integration test) uses a query string engineered to match
nothing (a long, obviously-arbitrary token), confirmed empirically to return
zero results regardless of what mail exists on the machine running the
test — so the real subprocess path is exercised without ever reading or
printing real personal email content.

### Safety note: `write_setting` tests never touch real preferences

`write_setting`'s curated settings point at real domains (`com.apple.finder`,
`com.apple.dock`, `NSGlobalDomain`, `com.apple.screencapture`). Any test that
actually *writes* through `write_setting` therefore uses a synthetic
`defaultsAllowlist` entry pointing at an absolute path under `t.TempDir()` —
confirmed that `defaults` treats an arbitrary file path as a one-off domain —
inserted for the duration of one test and removed via `t.Cleanup`. The only test
that touches a real curated setting (`finder_show_hidden_files`) stops at
**staging**, which only performs a read-only `defaults read` probe; it never
calls `execute`, so the developer's or CI machine's actual Finder setting is
never written.

This is a fairly classic test pyramid for this kind of system: pure-data/pure-function
layers get exhaustive unit coverage, and the top layer gets a smaller number of
high-value, protocol-real integration tests rather than re-testing every
capability through MCP. There's no live macOS smoke test (piping JSON into the
binary by hand) because the server exits on stdin EOF before flushing — that's a
known, accepted limitation (`docs/issues/issue-stdio-smoke-test-unreliable.md`).

What this suite does **not** cover: whether a model picks the right tool call for
a given natural-language prompt. That is a model-selection concern, not an
engine-correctness one — see the separate eval harness below.

## Eval harness (`internal/evals`, NOT part of `go test ./...`)

Built per `docs/issues/issue-need-eval-harness-for-tool-selection.md` (now
resolved). Unlike everything above, this calls a real Anthropic model and is
therefore **not free, not deterministic, and not run automatically** — it's a
separate command (`go run ./cmd/runevals`, or `-dry-run` for the zero-cost
validation-only path) documented in full in the
[Evals](architecture.md#evals) section of `docs/architecture.md`.

What IS unit-tested without any network call or API key (so it does run under
plain `go test ./...`):

| File | What's being checked |
|---|---|
| `internal/evals/case_test.go` | JSON case loading: single-turn (`prompt`) vs. multi-turn (`turns`) resolve correctly; both-set and neither-set are rejected; duplicate IDs across files are caught; load order is sorted/deterministic; non-`.json` files in the cases directory are ignored. |
| `internal/evals/runner_test.go` | `CheckExpectation`'s pure assertion logic against hand-built `TurnOutcome` values: tool/operation matching, the `forbid_tools` auto-confirm guard (the harness's core safety check), and `text_contains` substring checks — all independent of the live agent loop in `runner.go`, which can only be exercised against a real model. |

The live agent loop itself (`runner.go`'s `RunAll`/`runCase`/`runTurn`) is
exercised only by actually running `go run ./cmd/runevals` against the 18
cases in `evals/cases/*.json` — there is no mock-model unit test for it, since
the entire point is measuring real model behavior. One case
(`find_then_count_uses_pipeline`) specifically checks the model reaches for
`pipeline` when no named operation covers the request; several others assert
`forbid_tools: ["pipeline"]` to catch the model over-using it when a named
operation already suffices.

`evals/cases/mail.json` carries the suite's strictest safety constraint:
`send_mail_stages_only_never_executes` is single-turn with `forbid_tools:
["execute"]` and is never followed by a confirmation turn, so a live eval run
can never reach `execute` for the one irreversible capability in the
registry. `search_mail_selection` dictates the exact (nonsense) search term
in its prompt so the live run is safe to actually execute without surfacing
real email content to the Anthropic API.
