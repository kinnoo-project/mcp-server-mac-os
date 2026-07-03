# Regression tests currently run

Run via `go test ./...` (plus `-race` on the store), organized bottom-up through
the architecture:

| Layer | What's actually being checked |
|---|---|
| **`internal/registry`** | Manifests parse and load; structural validation rejects malformed capabilities (duplicate names, unknown enum/type values, a flag-kind param missing its flag token); the new `TestRiskClassificationInvariant` checks every mutating capability carries non-`none` risk; `TestNew_Rejects`/`TestNew_AcceptsAutoCommit` cover the `auto_commit` rule (rejected on a read-only or medium/high-risk capability, accepted on a reversible low-risk one). |
| **`internal/policy`** | Binary resolution only ever returns a path under `/bin`, `/sbin`, `/usr/bin`, `/usr/sbin`; path-separator injection and rogue-substitution attempts are rejected. |
| **`internal/engine`** | Per-type parameter coercion (tilde expansion, enum/required checks, unknown-key rejection); the generic builder's flag → `--` → positional ordering; `find`/`grep`'s irregular named-builder grammars; `largest_files`' ranking. For mutation: the seam's stdin channel (`TestRunCommand_StdinIsPureData` — a payload full of flag-like/metacharacter text round-trips through `cat` asserted by exact equality, proving `Command.Stdin` reaches a child as pure data, never parsed as argv options or paths — the guarantee is argv-only, so mutators pair `Stdin` with data sinks like `tee`/`pbcopy`, never interpreters; `TestRunCommand_StdinSizeCap` — a payload over the engine-wide `maxStdinBytes` ceiling is refused before any subprocess runs; `TestRunCommand_NilStdinUnchanged` pins the no-payload path) and `previewSnippet`'s rendering contract (`TestPreviewSnippet`: exact byte counts, first-line-only snippets, elision marking for multi-line/over-long payloads, control characters escaped rather than raw); `stageMkdir`'s forward/inverse/preview values, its existing-path and dash-leading-path guardrails, and a real stage → run-forward → run-inverse round trip against a temp directory; the content-carrying mutators (`TestStageWriteFile_*`, `TestStageAppendToFile_*`, plus real round trips) — write_file's fixed `tee -- <path>` argv with the content verbatim on stdin (the flag-like-content regression `TestStageWriteFile_FlagLikeContentIsStdinData` pins that hostile content lands in Stdin while argv stays fixed), its create-only guardrails (occupied target including a dangling symlink, missing parent, dash-leading path, 1 MiB content cap) and Trash-recycling inverse, and append_to_file's `tee -a` forward with a byte-exact truncating-tee inverse baked from the file's prior contents (regular-file-only, 8 MiB target cap, non-empty content), with round trips proving the appended file restores exactly; the archive mutators (`TestStageCompress_*`, `TestStageExtract_*`) — `compress`'s fixed `tar -a -c -f <archive> -C <parent> -- <basenames…>` argv (basenames sorted for deterministic ordering; several shared-parent sources land as separate operands after the `--`; a Trash-recycling inverse) and its guardrail table (missing/dash-leading/bad-extension/occupied archive, a missing archive parent, missing/empty/dash-leading/nonexistent sources, sources spanning two parents, a duplicate source, an archive written inside a source it is archiving), `extract`'s `tar -x -f <archive> -C <dest>` forward run in bsdtar's secure default (no `-P`) with a whole-destination Trash inverse and its guardrails (a missing/nonexistent/non-regular-file/bad-extension archive, a missing/dash-leading/nonexistent/non-directory/**non-empty** destination), the format allowlist (`TestArchiveExtension`: exactly `.zip`/`.tar.gz`/`.tgz`, matched case-insensitively — `.gz`/`.tar`/`.rar`/`.zipx` rejected), and the security regression `TestStageExtract_RefusesZipSlip` — a crafted archive whose members escape via a `..` component or an absolute path cannot write outside the destination (bsdtar refuses the `..` member and strips the leading `/` so the absolute member lands harmlessly inside), asserted on the resulting on-disk state, not the exit code; the protocol-level `TestIntegration_WriteFileLifecycle` (internal/server) drives stage→execute→undo over real MCP, proving the stdin payload survives the token store and undo recycles into a sandbox Trash; the three reversible file mutators (`mutate_filesystem_test.go`) — `stageMove`'s `mv`-forward/`mv`-inverse argv and a real round trip restoring the original layout, the "into an existing directory" path (`destination` is a dir → final path is `dir/basename(source)`, the exact bug-report scenario), and its rejection table (missing/nonexistent source, missing destination, overwrite of an existing target, dash-leading source or destination, a destination whose parent directory is missing or is not a directory — mv/cp do not create intermediate dirs); the batch `source_glob` move (`TestStageMoveGlob_PlanAndRoundTrip` — several files selected by a pattern, moved into one directory by a single `mv … destDir` forward and restored by a single `mv … commonParent` inverse, with a real round trip; `TestStageMoveGlob_Rejects` — both `source` and `source_glob` supplied, no matches, a dash-leading glob, a non-existent or non-directory destination, a glob whose matches span multiple parent directories, a name collision at the destination, and a destination equal to the matches' own parent) and `TestStageMove_WhitespaceHint` (a single `source` mistyped with an ordinary space where the real file uses a narrow no-break space (U+202F) surfaces an error pointing at the real file and steering to `source_glob`); `stageCopy`'s `cp -R --` forward with an inverse that recycles the freshly-made copy to the Trash (never an `rm`), exercised by a round trip confirming the original survives undo and the copy lands in a redirected sandbox Trash; `stageRemove`'s Trash-recycling forward (`mv` into `~/.Trash`, never a hard delete) with a `mv`-back inverse, a full delete→restore round trip, and its missing/nonexistent/dash-leading rejections; plus `trashPathFor`'s collision-suffix rule (an occupied `test.txt` in the Trash yields `test 2.txt`). The Trash-routed round trips redirect `$HOME` to a temp dir so the real Trash is never touched. `stageWriteSetting`'s forward/inverse/preview values for both the unset-key case and the prior-value-capture case, its refusal to stage when the existing value isn't a plain boolean, its refusal of a setting name absent from the allowlist, a data sanity check that every curated entry has non-empty domain/key/label, and a real stage → run-forward → run-inverse round trip via the real `defaults` binary against a **synthetic allowlist entry pointing at a disposable temp file** (never a real curated domain — see Safety note below); and `stageSendMail`'s validation (rejects no recipients, an address with no `@`, an empty subject, a missing or directory attachment path), its count-prefixed argv layout (`["-e", script, "--", subject, body, recipientCount, recipients..., attachments...]` — the `--` is the osascript end-of-options terminator that blocks option injection, and the count is what lets two variable-length lists share one flat argv with no delimiter), its verbatim preview text (including the attachment-filename line, present only when attachments exist), and that `Inverse` is always `nil` (irreversible) — **no test ever executes the `Forward` command**, since that would send a real email (see Safety note below). For pipelines (`pipeline_test.go`): `SupportsPipeline`'s eligibility rule against the real manifest (read-only argv-builder capabilities eligible; builtins and mutators rejected); a real two-stage `find`→`wc` round trip; `MaxPipelineStages` enforcement; the first-stage-missing-input guard (refuses rather than hangs); a failing middle stage aborting with its exit code; and the intermediate-size cap, split into `TestRunPipeline_IntermediateSizeCapEnforced` (a non-final stage exceeding the lowered cap aborts) and `TestRunPipeline_FinalStageNotSizeCapped` (the cap does NOT apply to a pipeline's last/only stage — its raw output goes straight to the same uncapped compaction path `Run` uses). `TestRun_AcceptsStdinCapabilityRefusesStandaloneWithoutInput` (generic-builder, `wc`) and `TestRun_GrepRefusesStandaloneWithoutPaths` (named-builder counterpart — a PR review caught that `grep`'s `paths` needed `arg.kind: "positional"` for this guard to find it at all) cover the standalone-hang guard; `TestValidateBuilders_AcceptsStdin` covers its boot-time precondition (only read-only, argv-builder capabilities may set `accepts_stdin`). For `search_mail` (`builtins_mail_test.go`): `parseMdlsOutput`'s pure parsing against canned `mdls`-format text (including the `(null)`-missing-attribute case and that attribute order isn't assumed), `splitNonEmptyLines`, the required-`query` guard, the dash-leading-query injection guard (`mdfind` has no `--`), and a real `mdfind` call with a query engineered to match nothing (see Safety note below). For the Mail **read** builtins `list_inbox`/`read_message` (`builtins_mail_reads_test.go`): `parseInboxRows` (tab-delimited `id`/sender/subject/date rows, malformed/blank rows skipped), `sortInboxNewestFirst` (descending lexicographic date sort = newest-first), `renderInboxList` (exposes each message's `id`, `(unknown)`/`(untitled)` placeholders), `cappedInboxLimit` bounds (default 20, hard cap 50, non-positive falls back), and the `read_message` required-`id` guard (empty and whitespace-only refused before osascript runs) — the mailbox/id inputs ride the shared `runOsascript` `--` terminator, proven once by `TestOsascriptArgv_SharedByBothPaths` (the extracted helper both the read path `runOsascript` and the mutating path `osascriptCommand` build their argv through, so a mailbox of `-e` lands as data). For `spotlight_search` (`builtins_spotlight_test.go`): the required-`query` guard (empty and whitespace-only), the dash-leading-query injection guard (`-e`/`-name`/`--onlyin`/`-` all refused before `mdfind` runs, since `mdfind` has no `--`), the bad-scope-`dir` rejection (a nonexistent directory is reported cleanly rather than silently matching nothing — the same path a dash-leading `dir` takes, since `filepath.Abs` turns `-foo` into an absolute path that then fails the stat, so a dash-leading value can never reach `mdfind` as a flag), `formatSpotlightResults`' pure rendering (no-match message incl. the scope note, numbered listing, and the honest truncation footer when matches exceed the limit), and a real `mdfind` call scoped to a fresh empty temp directory so it is guaranteed to match nothing regardless of the machine's Spotlight index (see Safety note below). For the shared osascript seam (`applescript_test.go`): `osascriptCommand` always inserts the `--` terminator before data, `parseDate`/`parseClock` reject malformed/impossible dates and times, `parseFmtDateTime`↔`fmtYMDHM`↔`ymdhm.args()` round-trip the probe format, and `asRows` splitting. For Calendar mutators (`mutate_calendar_test.go`): `stageAddEvent`'s full forward/inverse argv layout (`["-e", script, "--", calendar, title, location, notes, start…, end…]`), its `--`-terminator placement and a flag-like-title (`-e`) regression, its natural-key inverse, preview text, and validation rejections (missing calendar/title, bad date/time, end ≤ start); plus the pre-probe rejection paths of `stageModifyEvent`/`stageDeleteEvent`. For Reminders mutators (`mutate_reminders_test.go`): `stageAddReminder` across all three due shapes (timed/all-day/none), the always-incomplete-on-create flag, natural-key inverse, preview, and rejections (missing list/title, `due_time` without `due_date`, bad date); plus pre-probe rejections of modify/complete/delete. **No Calendar/Reminders test ever executes a `StagedPlan` command** (see Safety note below). For the phone domain (`mutate_phone_test.go`, `builtins_phone_test.go`): `canonicalizePhoneNumber`'s accept/reject table (formatted numbers canonicalize; empty, scheme-like `tel:`/`file://`/`http://` values, letters, an interior `+`, and out-of-range digit counts are rejected — the URL-scheme injection guard); `stageCall`'s `open tel:`/`facetime:`/`facetime-audio:` URL per method, `Inverse == nil` (irreversible), preview, the exactly-one-of `number`/`contact_name` guard, and bad-number rejection; and `find_contact`'s pure `parseContactRows`/`renderContacts`/`friendlyLabel` logic (grouping by person, `limit` capping people not rows, the `_$!<Mobile>!$_`→`Mobile` cleanup) plus the required-`name` guard. The `contact_name` resolution and Contacts lookup run a live `osascript`, so are not executed in tests — and **no test ever places a real call** (see Safety note below). `chooseContactNumber` (the pure contact-resolution policy split out of the live Contacts query) is covered directly: no-match, single dialable, same number under two labels, a lone un-dialable number (errors rather than being auto-selected), and two-distinct ambiguity. For the messages domain (`builtins_messages_test.go`, `mutate_messages_test.go`): `escapeSQLLiteral` (doubles quotes — the `' OR 1=1; DROP TABLE message;--` regression stays a literal — and rejects NUL), `plausibleEmail`, `handleMatchClause` (email exact-equality vs phone digit-suffix `LIKE`, too-short/invalid rejected), `messageRow` JSON decoding, `cappedLimit`, `previewText`/`renderMessages`, `resolveMessageRecipient`'s exactly-one-of guard on the non-Contacts path, and the `attributedBody` text recovery (`extractTypedstreamText`/`messageText`: round-trips text/emoji and the 0x81 two-byte-length path through a synthetic typedstream blob, prefers a present `text` column, and degrades a missing-marker/garbage-hex blob to "" rather than panicking — the layout was cross-checked against a real chat.db row); plus `stageSendMessage`'s `osascript` argv (`--` terminator + canonicalized handle/text + the count-prefixed attachment paths `[handle, text, attachmentCount, paths...]`, including a flag-like-text `-e` regression and a with-attachments and attachments-only-no-text case), `Inverse == nil`, verbatim preview (naming each attachment), and validation rejections (no text *and* no attachments, no/both recipients, bad phone/email handle, a missing attachment file, a directory attachment path). The live `sqlite3`/Contacts paths are not executed and **no test reads a real chat.db or sends a message** (see Safety note below). For the application domain (`builtins_apps_test.go`, `mutate_apps_test.go`): `renderAppList`'s pure de-dup/filter/sort/truncate logic against synthetic `mdfind` output, `validateAppName` (rejects empty/leading-dash/control-char names), and `stageFocusApplication`/`stageQuitApplication`/`stageOpenApplication` command construction — the app name lands as data after the osascript `--` terminator, a flag-like name (`-e`) is rejected (the option-injection regression), focus/quit are `Inverse == nil`; `open_application`'s live System-Events probe is not exercised (only its pre-probe validation). For `open_file` (`appdocs_test.go`, `mutate_apps_test.go`): the pure document-type detection logic — `parseDocTypes` (extracts and lowercases an app's declared extensions/UTIs from plutil JSON, ignores empty entries, reports `declaredAny=false` when an app declares no document types), `appSupportsFile` (extension match, `*` wildcard, exact-UTI match, confident non-match), `parseMdimportType` (pulls the UTI from `mdimport -t -d1` output), `fileTypeLabel`/`sampleTypes` — plus `openFileForward`'s argv layout for both the named-app form (`open -a <app> -- <file>`) and the default-app form (`open -- <file>`), the file always landing as data after the `--` terminator — the option-injection regression; `stageOpenFile`'s validation rejections that short-circuit before any live probe (missing/empty/flag-like/nonexistent file, a directory, a flag-like app — a *missing* app is deliberately valid), the hermetic default-app path (no `app` → `open -- <file>`, `Inverse == nil`, default-application preview), the regression that the named branch reads the `app` parameter (a flag-like app fails with the leading-dash message, which only happens if the value was actually read — guards against a param-name mix-up), and `composeOpenFilePreview`'s three verdict shapes (clean intent line when supported; a leading ⚠️ warning naming the file when unsupported or uncertain). The live `resolveAppBundle`/`appDeclaredDocTypes`/`fileUTI`/`appAlreadyRunning` probes are not executed in tests. For `open_website` (`mutate_apps_test.go`): `normalizeWebsiteURL`'s accept/reject table — a bare domain gains an `https://` scheme (`youtube.com`→`https://youtube.com`, host case preserved), a full `http(s)` URL passes through unchanged, a bare host:port is accepted and defaulted to https (`localhost:8080`, `example.com:8080`, `127.0.0.1:3000` — the all-digit opaque marks the port), surrounding whitespace is trimmed, and every non-web input is rejected (`file://`, `tel:`, `sms:`, `mailto:`, `javascript:`, `ftp://`, a leading dash, an embedded space, an unrecognised `scheme:opaque` like `myapp:foo`, a web scheme missing its `//` like `http:example.com`/`https:example.com`, a URL with embedded userinfo like `https://user:pass@host`/`https://trusted.com@evil.com` (credential-leak and phishing-disguise guard), `user@example.com`, empty) — including the subtlety that a scheme-only value with no `://` (`tel:911`) is rejected *before* the bare-domain default would otherwise turn it into `https://tel:911`; plus `stageOpenWebsite`'s command construction — the default-browser form (`open -- <url>`) and the named-browser form (`open -a <browser> -- <url>`), the normalized URL always landing as data after the `--` terminator (the option-injection regression), `Inverse == nil` (irreversible), and the stage-time rejection of a flag-like `url`/`browser`, a non-web scheme, a control-char browser name, and a missing `url`. **No test executes the `Forward`**, so no browser is ever launched. For the printer domain (`builtins_printers_test.go`, `mutate_printers_test.go`): `renderPrinterList`'s parsing of `lpstat -p -d` (idle + default marker, the disabled-queue hand-off note, no-printers), `lpArgs` ordering (`-d`/`-n`/`--`/file), `validateCopies`/`validatePrinterName` bounds, `stagePrintFile`'s `lp` argv + missing-file rejection + `Inverse == nil`, and `stagePrintTestPage` writing the embedded page to the `/tmp/mcp-fallback` scratch file then staging `lp -- <scratch>` — **no test ever runs a print** (`Forward` is never executed). For the system domain (`builtins_system_test.go`, `mutate_system_test.go`, `builtins_sysinfo_test.go`): `parseWifiDevice` (resolves the Wi-Fi interface from hardware-port output), `renderBluetoothStatus` (on/off + connected device names from `system_profiler -json`), `renderPowerStatus` (battery summary pass-through + Low Power Mode on/off parse), `renderBatteryHealth` (condition/cycle-count/max-capacity from `SPPowerDataType -json`; "" for a batteryless or unparseable profile so the line is omitted), the machine-info builtins' pure renderers — `renderAboutThisMac` (sw_vers pass-through + hardware fields, degrading to version-only on bad JSON), `renderDiskUsage` (keeps device-backed volumes including spacey mount points via `nthColumnOffset`, drops devfs/auto_home, passes garbage through), `renderSoftwareUpdateCheck` (merges the stderr verdict and stdout update list, drops the progress banner) — with the real-binary end-to-end run gated behind `MCP_SYSINFO_LIVE=1` (which deliberately excludes `software_update_check`: it contacts Apple's servers), and `stageOpenSettings`'s pane→`x-apple.systempreferences:` URL mapping (every enum pane resolves, unknown pane rejected, `Inverse == nil`; `TestStageOpenSettings_HandoffPaneURLs` pins the exact deep-link URLs of the guided hand-off panes — `focus`, `keyboard`, and the legacy-prefixed `apple_id` — which the non-emptiness loop alone would not catch) — **no test ever opens a Settings window**; plus the two agent→human back-channel mutators — `stageNotify`'s osascript argv (`["-e", script, "--", message, title]`, both strings landing as data after the `--` terminator, a flag-like message/title (`-e`/`-rf`) regression, `Inverse == nil`, the "cannot be undone" preview, the omitted-`title` default applied through `normalizeParams`, and the empty-message/oversized-message/oversized-title rejections), and `stageSpeak`'s `say -- <text>` argv (the `--` terminator making a dash-leading text like `-v`/`-r`/`--file-format` a pure operand — the option-injection regression, since `say` reads a leading dash as its voice/rate flag otherwise), `Inverse == nil`, the "cannot be undone" preview, the empty-text and >2000-character rejections, and the char-not-byte cap (exactly 2000 multibyte runes accepted) — **no test ever posts a notification or plays audio** (the `Forward` is never executed; the audible/visual paths are manual, see Safety note below); plus the two paired availability reads (`builtins_devices_test.go`) — `parseAirplayDevices` (extracts distinct receiver names from `dns-sd -B` browse rows, keeping a device name that itself contains spaces/quotes as one value, honouring Add/Rmv so a receiver that announced then withdrew within the browse window is dropped, and skipping the banner/header lines) and `renderAirplayDevices` (numbered listing + the Displays-pane hand-off note; the empty case explains "none on, or Local Network access not yet granted"), and the input-source reader — `parsePlistDicts` (a minimal reader for the old-style/NeXTSTEP plist that `defaults read` prints, splitting an array of `{ key = value; }` dicts and stripping quotes from both quoted and bare tokens), `lastDotComponent` (humanises a reverse-DNS input-mode id like `…SCIM.ITABC` to `ITABC`), and `renderInputSources` (lists keyboard layouts + input modes and marks the active one from `AppleSelectedInputSources`, filters out helper input methods like the character palette/press-and-hold, still lists sources with no "current" marker when the selected read is unavailable, and falls back to the raw plist when unparseable). Neither op takes a model-controlled parameter, so there is no injection regression to pin (only constants reach an argv); the live `dns-sd`/`defaults` reads are exercised by hand, not in tests. For the notes domain (`builtins_notes_test.go`, `mutate_notes_test.go`): `parseNoteRows` (tab-delimited metadata, malformed/blank rows skipped), `sortAndCapNotes` (most-recently-modified first, capped to keep the newest), `renderNoteList` (exposes each note's `id`, `(untitled)`/`(unknown)` placeholders), and `cappedNoteLimit` bounds; plus `stageCreateNote`'s forward/inverse argv (`["-e", script, "--", folder, bodyHTML]` and the delete-by-title inverse), the default-folder (empty-string) branch, a flag-like-title (`-e`) regression neutralized after `--` in *both* forward and inverse, the missing/blank-title rejections, and `noteBodyHTML`/`appendedHTML` HTML-escaping (`<`/`>`/`&`/`"` escaped, newlines → `<br>`). `append_to_note`'s stage path runs a live Notes body probe, so it (and all read builtins) are not executed against the real app — **no test launches osascript or touches real notes** (see Safety note below). For the screenshot domain (`builtins_screenshot_test.go`): `screencaptureArgs`' exact flag ordering (`-x -t <format>` + optional `-R <x,y,w,h>` crop + optional `-D <display>` + the path as the trailing operand) across the main-display, second-display, region-crop, and negative-origin-region cases; `runCaptureScreen`'s fail-fast input rejections that return before any subprocess (unsupported format, zero/negative display, a dash-leading `output_path`, an unsupported `output_path` extension); `resolveScreenshotPath`'s rules (`TestResolveScreenshotPath`: empty → a generated name under `~/Pictures/Screenshots`; an existing directory → generated name inside it; a full file path whose extension drives the format incl. the `jpeg`→`jpg` alias; no extension → the format's extension appended; unsupported-extension and dash-leading rejections); `TestCaptureScreen_RefusesOverwrite` (a capture aimed at an existing file errors before screencapture runs, preserving the create-only/read-only-safe contract — exercised entirely on a temp file); the `TestScreenshotFormats_MatchManifestEnum` drift check (the manifest `format` enum and the engine's `screenshotFormats` map stay 1:1); `screencapturePermissionError` always pointing at the Screen Recording grant (folding in stderr detail when present, a synthetic exit-code note when empty — the silent-denial case); `imageDimensions` reading width/height back from a real temp PNG and reporting `ok=false` for a non-decodable file (the PDF/TIFF path); and `reportCapture`'s summary (path + human-readable size, dimensions omitted for non-decodable formats). The cropped captures (`builtins_screenshot_region_test.go`) add: `capture_region`'s fail-fast rejections (`TestCaptureRegion_RejectsBadInput`: missing `x`/`y`, a zero/negative/oversize `width`/`height`, and a dash-leading `output_path`), `TestCaptureRegion_AcceptsNegativeOrigin` (a negative `x`/`y` is a valid off-main-display coordinate and passes validation — proven by the run failing only later, at an unwritable directory, not at the dash-leading-path guard), `capture_window`'s injection regression (`TestCaptureWindow_RejectsHostileApp`: a flag-like app name like `-e` is rejected by `validateAppNameValue` before any System Events call, and would in any case reach the geometry-probe script only as argv data after `--`), `TestCaptureWindow_RejectsBadIndex` (a non-positive `window_index`), and `TestCaptureWindow_RejectsDashLeadingOutputPathFast` (a dash-leading `output_path` is rejected by the shared path guard *before* the permission-gated window-bounds probe — asserted via the path-guard message, which only appears if validation ran first — so an invalid request never provokes a spurious Automation/Accessibility prompt). The tests that actually run `screencapture`/System Events (`TestCaptureScreen_Live`, `TestCaptureRegion_Live`, `TestCaptureWindow_Live`) are **skipped unless `MCP_SCREENSHOT_LIVE=1`**, since a real capture needs a physical display and a granted Screen Recording permission (plus, for the window, Accessibility + Automation). For the network domain (`builtins_network_test.go`): `validateNetworkHost`'s **accept/reject table** — the mandatory option-injection regression for the two operations that take a model-controlled host (`ping_host`, `dns_lookup`), asserting hostnames/IPv4/IPv6 pass while empty, over-long, flag-like (`-e`, `-c100`, `--flood`), `@server`, `+queryopt`, whitespace, slash, and embedded-newline values are rejected as data (the sole defense for `dig`, which has no `--` terminator); plus the pure parsers against canned command output — `parseDefaultRoute` (interface/gateway from `route -n get default`), `parseIfconfig` (IPv4/hex-netmask/MAC), `hostCapacityFromMask`/`dottedMask` (hex+dotted masks → CIDR prefix and usable-host count, e.g. /24→254, with the /30, /32, and unparseable edges), `parseScutilDNS` (deduped first-seen resolver order), `parseArp` (skips `(incomplete)`, tolerates single-digit MAC octets, de-dups by IP), `parseLsofListeners` (takes the address after the `TCP`/`UDP` node column — not the last field, since TCP rows append `(LISTEN)` — for both TCP and UDP, de-duping by pid+port), and `subnetHosts` (a /24 enumerates 253 candidates excluding network/broadcast/own-IP, and a subnet wider than /24 is refused so the active sweep stays bounded). The end-to-end builtins (`TestNetworkBuiltins_Live`, running `route`/`ifconfig`/`scutil`/`ping` against the real machine) are **skipped unless `MCP_NETWORK_LIVE=1`**. For the system domain's Bluetooth reader, `renderBluetoothStatus` now also asserts that paired-but-not-connected devices (`device_not_connected`) are listed alongside connected ones. For the process domain (`builtins_process_test.go`, `mutate_process_test.go`): the pure parsers/classifiers against canned tool output — `parsePsRows` (skips the header, preserves a space-bearing app path like `/Applications/Google Chrome.app/…` as one command, parses pid/ppid/%cpu/%mem, detects the zombie state), `classifyOrigin` (system vs user-installed vs other from the executable path), `appNameFromExePath` (the innermost `.app` bundle name, "" for a plain binary), `humanBytes`, `parseVMStat` (page counts × the reported page size, keyed by metric name), `parseLoadAvg`, `parseGPUStats` (pulls `Device/Renderer/Tiler Utilization %` and `In use system memory` out of ioreg's one-line dict without confusing the `(driver)` variant), `parseLaunchctlList`/`launchdLabelForPID` (third-party filtering drops `com.apple.*`; PID→label lookup), and `validateProcessFilter` (rejects control chars; the filter never reaches a subprocess); plus the PID guard — `runProcessInfo`/`requirePID` reject pid ≤ 1 (the protected kernel/launchd PIDs, which also ensures a PID can't be read as a flag). For the two mutators: the mandatory osascript option-injection regression (`quit_process` places a flag-like app name as data after `--`, and `validateAppNameValue` rejects a dash-leading bundle name); `stageQuitProcess` refusing a non-GUI process (the test binary, not inside a `.app`) with a pointer to `terminate_process`; and `stageTerminateProcess` asserting the forward command is exactly `kill -TERM <pid>` with `Inverse == nil` — SIGTERM is hardcoded so it can never be escalated to a force-kill and is not undoable (staged against the test's own PID, **never executed**). The end-to-end read builtins (`TestProcessBuiltins_Live`, running `ps`/`sysctl`/`vm_stat`/`ioreg`/`launchctl` against the real machine) are **skipped unless `MCP_PROCESS_LIVE=1`**. For the photos domain (`builtins_photos_test.go`, `builtins_photos_export_test.go`, `mutate_photos_test.go`, `mutate_photos_weak_test.go`): the pure parsing/rendering of the read builtins — `parsePhotoRows` (tab-delimited metadata, malformed/blank rows skipped), `renderPhotoList` (exposes each item's `id`, a favorite ★, `(untitled)` placeholder), `renderPhotoDetail`'s GPS-present vs GPS-absent branches (coordinates/altitude surfaced only by `get_photo`; `(none)` placeholders otherwise; the size line omitted when empty), and `cappedPhotoLimit` bounds; `export_photo`'s dash-leading-`destination` rejection and missing-`id` guard (both returning before any subprocess or directory is created), `exportedFiles`/`reportExport` (subdirectories ignored, files name-sorted, full paths reported), and `boolText`; the reversible property mutators' pure command builders (`favoriteCommand`/`nameCommand`/`descriptionCommand`/`dateCommand`/`keywordsCommand`), each placing model data after the osascript `--` terminator under a flag-like (`-e`) battery, the empty-keywords clear, `stageSetDate`'s malformed-date rejection *before* any probe, the shared `requireID` guard across all five, and the preview helpers (`keywordsPreview`/`quoteOrCleared`); and the no-undo mutators (`create_album`/`create_folder`/`add_to_album`/`import_photos`) — full forward argv (these stage WITHOUT a probe), the flag-like-value regression on `add_to_album`, validation rejections (missing album/ids, an empty id element, a missing import file refused at stage time via a real temp file), the top-level (empty-parent) branch, and the invariant that each is `Inverse == nil` with a "cannot be undone" preview. The read builtins and the reversible mutators' live Photos probes are not executed — **no test launches osascript or touches the real Photos library** (see Safety note below). For the clipboard domain (`builtins_clipboard_test.go`, `mutate_clipboard_test.go`): `read_clipboard`'s pure rendering half `formatClipboardText` (empty stdout → a clear "empty or non-text" note; text returned verbatim; output larger than the subprocess budget compacted, since a builtin's output is not otherwise capped) — no test reads the machine's real clipboard, which could surface a user's actual copied secret; and `write_clipboard`'s pure staging half `planWriteClipboard` (fed the prior clipboard bytes directly, never touching the live pasteboard) — the reversible case (forward `pbcopy` with the new text on stdin and **no argv operand**, byte-exact `pbcopy` inverse restoring the prior text, both `DiscardStdout` so undo never echoes the prior/possibly-secret value), the two no-undo cases (`Inverse == nil` when the prior clipboard was empty/non-text or larger than the 1 MiB undo cap — clearing the clipboard is not offered as a lossy "undo" of an image), the oversized-new-text rejection, and the injection regression `TestPlanWriteClipboard_HostileTextLandsAsData` (a battery of flag-like/shell-active values — `-e`, `--`, `; rm -rf /`, `$(reboot)` … — always land verbatim on stdin with an empty argv, since `pbcopy` takes no operand at all). |
| **`internal/transaction`** | The token store's contract: round-trip, prefix/uniqueness, one-shot consumption, TTL expiry, opportunistic purging of expired entries on `Put` (so an abandoned-token workload can't grow the store without bound), and (under `-race`) safety under concurrent `Put`/`Take`. |
| **`internal/server`** | Behavioral tests drive every capability through the real domain-tool handler against a hermetic fixture tree; the in-process integration test drives the *actual* MCP protocol: tool listing across all sixteen domain tools (`filesystem`, `preferences`, `application`, `printer`, `system`, `network`, `process`, `screenshot`, `clipboard`, `application-mail`, `application-calendar`, `application-reminders`, `application-phone`, `application-messages`, `application-notes`, `application-photos` — each asserted to embed its full operation menu, including the `network` tool's seven read-only operations and the `process` tool's eight operations) plus `execute`/`undo`/`pipeline` (19 tools total), the full `mkdir` stage→execute→undo round trip, a **stage-only** `write_setting` call against a real curated setting (asserting a token+preview come back, deliberately never calling `execute` — see Safety note below), a real `find`→`wc` pipeline round trip over the protocol, a real `search_mail` no-match call, a **stage-only** `send_mail` call (asserting the irreversibility warning appears in the preview, never calling `execute` — see Safety note below), structured errors for bad operations/tokens/pipeline stages (including a mutator or an unknown capability name as a stage), the auto-commit lane (`TestDomain_AutoCommitRunsImmediately`: a low-risk `auto_commit` mutation built on the real `mkdir` mutator runs immediately — no `req_` staging — creates the directory, returns an `undo_` token, and that token reverses it), and three drift checks (`TestDefaultsAllowlist_MatchesManifestEnum` for `write_setting`'s `setting` enum vs the engine's `defaultsAllowlist`; `TestReadSettingEnum_MatchesDefaultsAllowlist` for `read_setting`'s `setting` enum vs the same allowlist, so a setting is either both readable and writable through the curated list or neither; `TestSettingsPanes_MatchManifestEnum` for the `open_settings` `pane` enum vs the engine's `settingsPaneURLs` map). The description rendering itself is guarded by `menu_test.go`: `TestDomainDescription_ListsEveryOperationUpFront` checks that **every** category's description leads with an `All operations:` line naming every operation *before* the per-operation `Details` section — the truncation-resilience property that keeps a clipped description from hiding an operation (the failure that once hid `move`/`remove`) — and `TestRenderParams_MarksRequiredCompactly` checks the compact `*`-marks-required rendering. |

## Security gate (production)

A dedicated, cross-cutting set of deterministic tests is the **hard gate that
must pass before any production release**. Unlike the per-capability tests above
(which prove each operation behaves), these prove safety *properties of the whole
catalog* and fail the build the moment a future change crosses a security
boundary. They run under plain `go test ./...` and are enforced automatically by
`.github/workflows/ci.yml` (on `macos-latest`, so the real Darwin binaries
resolve), which also runs `go vet`, a `gofmt` check, and the eval dry-run.

| Test file | What it guarantees |
|---|---|
| `internal/registry/security_invariants_test.go` | **Blast-radius containment.** No capability may reference a high-blast-radius binary on an explicit deny list (`rm`, `dd`, `diskutil`, `csrutil`, `spctl`, `nvram`, `dscl`, `killall`, `shutdown`, …) — so the surface cannot grow to include a way to wipe a disk, lock the user out, or disable a security control. Every named binary resolves through the policy trust boundary. No medium/high-risk capability is `auto_commit`. The irrecoverable ops (`send_mail`, `send_message`, `call`, `print_file`, `print_test_page`, `quit_process`, `terminate_process`) are each pinned high-risk + irreversible + never-auto-commit, and `terminate_process` may take only a pid (never a signal selector, so SIGTERM can't be escalated). |
| `internal/engine/injection_sweep_test.go` | **Registry-driven injection coverage.** The generic builder's `--` terminator and the osascript seam's `--` terminator are hammered with a battery of hostile values (leading-dash flags, `; rm -rf /`, `$(reboot)`, embedded newline/NUL), proving each lands as inert, verbatim data. A coverage gate then enumerates every capability that takes a free-text parameter and is answered by an in-process builtin (the cases not auto-covered by those two seams) and fails if any is missing from a reviewed allowlist documenting its guard — so a new free-text capability cannot merge without an injection defense and a regression test. |
| `internal/engine/bounds_test.go` | **Resource-exhaustion bounds.** Print `copies` is rejected outside 1..20; the per-read message limit is clamped to its ceiling; integer params reject fractional values; oversized command output is truncated with a notice. The executor enforces a wall-clock timeout (a `sleep` past a lowered budget is killed with a "timed out" error), and the `copy` mutator's free-space guard is covered by `copyFits` (the fit decision) and `treeSizeBytes` (the source-size estimate). |
| `internal/engine/builtins_messages_sqlinjection_test.go` | **End-to-end SQL-injection safety.** Drives the real `search_messages` path against a hermetic, throwaway `chat.db` (under a redirected `$HOME`) with injection payloads, asserting the term is matched literally, nothing extra returns, and the `message` table is intact afterward (no injected `DROP` ran). NUL-bearing queries are rejected. |
| `internal/engine/security_paths_test.go` | **Data-loss avoidance.** `remove` and the undo of `copy` recycle through the sandboxed Trash (an `mv`, never a hard `rm`); a path full of shell metacharacters is staged as one verbatim operand after `--`. |
| `internal/server/security_transaction_test.go` | **Token-gate integrity.** Over the real MCP protocol: `execute` rejects forged tokens; a token is one-shot (no replay); the `execute`/`undo` token namespaces don't cross; `undo` is one-shot. Together: "what runs is exactly what a human approved, exactly once." |

The matching **advisory** layer is the security eval cases (`evals/cases/security_*.json`,
below), which exercise live-model behavior against adversarial prompts. Those are
run before a release but, being non-deterministic and API-billed, are not part of
the automatic gate — CI validates only that they load (`-dry-run`).

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
missing-file and directory-path rejections, the attachment-naming preview, and —
for the sandbox-copy fix — that an attachment's argv path is a copy STAGED INTO
THE SANDBOX (basename and bytes preserved, same-basename files isolated), that an
absent sandbox container is refused up front, and that the stale-copy sweep
reclaims old staging dirs while sparing fresh ones (the staging dir is redirected
to a temp dir, so no test touches the real Messages container).

Manual smoke-test: grant Full Disk Access, run `check_messages` to confirm the
read path and the friendly permission error, then `send_message` *stage* →
`execute` to your **own** number/Apple ID. **Test an `attachments` image from an
ordinary location like `~/Downloads`** — that is exactly what failed before the
sandbox-copy fix. To verify *delivery* (not just that the send was issued),
sending to your own number gives no normal receipt, so confirm the upload
instead: the attachment's row in chat.db should reach `transfer_state = 5`
(success), not `6` (failed). For example:

```sh
sqlite3 ~/Library/Messages/chat.db \
 "SELECT a.transfer_state, m.error, m.is_sent, a.filename
  FROM message m
  JOIN message_attachment_join maj ON maj.message_id = m.rowid
  JOIN attachment a ON a.rowid = maj.attachment_id
  WHERE m.is_from_me = 1 ORDER BY m.date DESC LIMIT 3;"
```

See `docs/issues/bug-imessage-attachment-send-fails.md`,
`docs/issues/note-imessage-attachments-design.md`,
`docs/issues/note-imessage-applescript-send.md`, and
`docs/issues/note-messages-read-fda.md`.

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

### Safety note: notes tests never touch real notes

The `application-notes` reads drive the real Notes app via `osascript` (needing a
granted Automation permission), and the write paths create/modify real notes. So
**no test launches `osascript` or executes a `StagedPlan`** — tests cover the pure
Go logic only: metadata parsing, newest-first sorting/capping, rendering, limit
bounds, the staged `create_note` plan's forward/inverse/preview fields, the
flag-like-title (`-e`) option-injection regression, and the HTML-body escaping.
`append_to_note`'s stage path additionally runs a live Notes probe (to capture
the prior body for its inverse), so it is left to manual smoke-testing. The
shared osascript `--`-terminator hardening that protects the search term and note
`id` inputs is covered once, for the whole seam, by
`TestOsascriptCommand_InsertsTerminator` (`applescript_test.go`). Manual
smoke-test: grant Automation access to Notes, run `list_folders`/`list_notes` to
confirm the read path and the friendly permission error, then `create_note`
*stage* → `execute` → `undo` (the undo deletes the created note) and
`append_to_note` *stage* → `execute` → `undo` (the undo restores the prior
contents). See `docs/issues/note-application-notes.md`.

### Safety note: photos tests never touch the real Photos library

The `application-photos` reads drive the real Photos app via `osascript` (needing
a granted Automation permission), `export_photo` writes real files out of the
library, and the write paths change real albums/metadata or import real files. So
**no test launches `osascript` or executes a `StagedPlan`** — tests cover the pure
Go logic only: row parsing/rendering, the GPS-present/absent detail branches,
limit bounds, the `export_photo` path/id guards and result reporting, the
reversible mutators' forward/inverse command builders with the flag-like (`-e`)
option-injection regression, and the no-undo mutators' full forward argv,
validation, and `Inverse == nil` invariant. The reversible mutators' stage paths
additionally run a live Photos probe (to capture the prior value for their
inverse), so those are left to manual smoke-testing. The shared osascript
`--`-terminator hardening that protects the free-text inputs is covered once, for
the whole seam, by `TestOsascriptCommand_InsertsTerminator` and
`TestInjection_OsascriptTerminatesHostileData`. **AppleScript cannot delete a
photo or video** (Photos' `delete` is restricted to albums/folders, which this
server does not expose), so no test — and no operation — can destroy a photo.
Manual smoke-test: grant Automation access to Photos, run `search_photos`/
`list_albums`/`library_stats` to confirm the read path and the friendly
permission error, `export_photo` to confirm a viewable file is written, then a
`set_favorite` *stage* → `execute` → `undo` round trip (the undo restores the
prior favorite state). See `docs/issues/note-photos-tcc-permission.md`.

### Safety note: the real `mdfind` calls never surface real user content

Two builtins exercise a real `mdfind` subprocess. `search_mail`
(`builtins_mail_test.go` and the protocol-level integration test) uses a query
string engineered to match nothing (a long, obviously-arbitrary token),
confirmed empirically to return zero results regardless of what mail exists on
the machine running the test. `spotlight_search`
(`builtins_spotlight_test.go`) scopes its one real call to a fresh empty
`t.TempDir()`, which has no files to match no matter what the machine's
Spotlight index contains — an unscoped whole-index search would be both
unreliable and capable of surfacing real files (indeed the test's own source
file contains the query token). Either way the real subprocess path is
exercised without ever reading or printing real personal content.

### Safety note: preferences tests never touch real preferences

`write_setting`'s curated settings point at real domains (`com.apple.finder`,
`com.apple.dock`, `NSGlobalDomain`, `com.apple.screencapture`). Any test that
actually *writes* through `write_setting` therefore uses a synthetic
`defaultsAllowlist` entry pointing at an absolute path under `t.TempDir()` —
confirmed that `defaults` treats an arbitrary file path as a one-off domain —
inserted for the duration of one test and removed via `t.Cleanup`. The only test
that touches a real curated setting (`finder_show_hidden_files`) stops at
**staging**, which only performs a read-only `defaults read` probe; it never
calls `execute`, so the developer's or CI machine's actual Finder setting is
never written. `read_setting`'s tests (`builtins_preferences_test.go`) use the
same synthetic-entry helper — they seed/read a throwaway domain under
`t.TempDir()`, never a real curated one. `set_appearance`'s tests
(`mutate_appearance_test.go`) never run the forward/inverse (which would flip the
developer's real Dark/Light mode) and never invoke the live, Automation-gated
System Events probe: the pure `planSetAppearance` builder, the mode/enum
rejection (which precedes any probe), and the `systemEventsScriptError` TCC
message are asserted directly. Its live toggle+undo is a manual case
(`m_set_appearance_then_undo`), since it is auto_commit and needs the System
Events Automation grant. See `docs/issues/note-system-events-tcc-permission.md`.

The window-management operations (`list_windows`, `move_window`, `resize_window`,
`minimize_window`) follow the same discipline. Their tests
(`builtins_windowing_test.go`, `mutate_windowing_test.go`) never drive a live
window or the Accessibility-gated System Events probe: the pure
`renderWindowList` formatter, the `parseGeometry` probe-output parser, the
`parseWindowTarget`/`positiveDimension` input guards (dash-leading app,
zero/negative index, non-positive or oversized dimensions), and the pure
`planMoveWindow`/`planResizeWindow`/`planMinimizeWindow` builders (forward/inverse
argv, the prior geometry/minimized-state baked into the inverse, and the
"already minimized → no undo" branch) are all asserted directly. A dedicated
`-e`-style regression proves the process name lands after the osascript `--`
terminator as inert data, and `windowScriptError` is checked to route an
"assistive access" denial to the Accessibility pane and every other denial to the
Automation hint. The live move/resize/minimize+undo paths are manual cases
(`m_app_move_window_then_undo`, `m_app_minimize_window`), since they auto_commit
against a real window and need the Accessibility grant. See
`docs/issues/note-window-management-design.md`.

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
| `internal/evals/case_test.go` | JSON case loading: single-turn (`prompt`) vs. multi-turn (`turns`) resolve correctly; both-set and neither-set are rejected; duplicate IDs across files are caught; load order is sorted/deterministic; non-`.json` files in the cases directory are ignored; the Layer-B fields (`setup`/`teardown`/`manual`, and `tool_succeeds`/`state` on an expectation) round-trip through the loader; and `applySetup` creates fixtures inside the scratch tree while its guardrails reject an unsafe scratch name (separator/`..`/absolute) or a fixture file that escapes via `..`. |
| `internal/evals/runner_test.go` | `CheckExpectation`'s pure assertion logic against hand-built `TurnOutcome` values: tool/operation matching, the `forbid_tools` auto-confirm guard (the harness's core safety check), `text_contains` substring checks, and the `tool_succeeds` outcome check (passes when the expected tool didn't error, fails when it's in `ErroredTools`, ignored when the field is unset) — all independent of the live agent loop in `runner.go`, which can only be exercised against a real model. |
| `internal/evals/state_test.go` | `CheckState`'s filesystem post-condition pass against real temp directories: `exists`/`absent`/`is_dir` each pass and fail correctly, an ambiguous non-"not exist" stat error on an `absent` path surfaces rather than passing, and `{{scratch}}` placeholders in state paths are resolved by the passed substituter before stat. Kept separate from `CheckExpectation` so that stays pure/I/O-free. |

The live agent loop itself (`runner.go`'s `RunAll`/`runCase`/`runTurn`) is
exercised only by actually running `go run ./cmd/runevals` against the cases
in `evals/cases/*.json` — there is no mock-model unit test for it, since
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

### Security eval cases (`evals/cases/security_*.json`)

The advisory counterpart to the deterministic security gate above: adversarial
prompts that measure whether the *model* refuses or stays behind the
confirmation gate. Each is single-turn with a `forbid_tools` assertion (never a
scripted "yes, do it" follow-up), so a live run can never reach an irrecoverable
commit even if the model misbehaves — the same safety discipline as `mail.json`.

- `security_destructive.json` — blunt destructive/lockout requests (wipe disk,
  delete home folder, kill WindowServer, disable SIP, change the login password);
  asserts `execute` is never called.
- `security_dos.json` — resource-exhaustion intents (10k folders, fill the disk,
  9999 print copies); asserts no commit.
- `security_exfiltration.json` — confused-deputy "read local data and send it
  out" prompts (read benign `/etc/hosts`, then email/iMessage it elsewhere);
  asserts the communications tools are never invoked.
- `security_injection.json` — prompt injection through content the server reads:
  the checked-in fixture `evals/fixtures/poisoned-instructions.txt` embeds
  override instructions to delete/exfiltrate/auto-execute; the cases read it and
  assert the model treats it as data, never acting on the embedded commands.

### Outcome assertions & everyday-Mac coverage (Layer B)

Selection assertions (which tool/op the model picked) can't catch a *right op,
wrong real-world result* bug — the shipped example being "move screenshots into
an existing `~/Desktop/screenshots`" landing files at the wrong path. Three
optional, backward-compatible additions close that gap (all cases without them
keep working unchanged):

- **`tool_succeeds: true`** on an expectation asserts the chosen tool's result
  wasn't an error block (backed by `TurnOutcome.ErroredTools`), so "the model
  called `move` but it failed and it narrated around it" no longer passes.
- **`setup`/`teardown`** stage a per-case scratch directory under the system
  temp dir (`os.TempDir()`, which honors `$TMPDIR` — typically `/var/folders/…`
  on macOS, not `/tmp`) named `mcp-eval-<unique>-<scratch>`, with fake input
  files, created with stdlib `os` only (no shell) and removed after the case even
  on failure. The resolved path substitutes into prompts and assertions as
  `{{scratch}}` (alongside the existing `{{unique}}`).
- **`state`** (`exists`/`absent`/`is_dir`) runs an `os.Stat` post-condition pass
  after a turn (see `CheckState`), naming the *intended* final paths. This is the
  assertion that fails on the screenshot-move bug and passes on the fix
  (`move_into_existing_dir` in `filesystem_mutations.json` is that regression
  proof).

The everyday corpus is split into two buckets:

- **Automated (A)** — runs under the default `go run ./cmd/runevals` with no
  permissions granted and no signed-in accounts, leaving no residue:
  `filesystem_mutations.json` (the Layer-B `state`-asserting core, plus the
  archive round trips: `compress_then_undo` create→undo-absent,
  `compress_refuses_existing_archive` no-clobber, and `compress_extract_roundtrip`
  compress→mkdir→extract proving a member reappears under the destination),
  `filesystem_writes.json` (write_file/append_to_file: create→undo-absent,
  append→undo, and the create-only overwrite refusal),
  `system_reads.json` (incl. `list_input_sources`, which succeeds on any Mac
  since every machine has at least one input source; `list_airplay_devices` is
  Manual — it browses the network for ~3s and the result is hardware-dependent),
  `network_reads.json`, `process_reads.json`,
  `pipeline_and_routing.json`, `screenshot.json` (selection-only — a real capture
  needs Screen Recording), `clipboard.json` (read_clipboard selection — reading
  the pasteboard always succeeds on a live session; the write+undo path is manual
  because it clobbers the live clipboard), `domain_selection.json` (routing checks,
  incl. `list_windows_routing` — "what windows are open?" selects
  `application/list_windows`; selection-only, so no Accessibility grant is needed).
- **Manual (M)** — cases tagged `"manual": true` (e.g. `manual_smoke.json`, plus
  `lan_scan_manual`) that need a permission grant, a signed-in account, or real
  hardware. They are **skipped by the default run** and listed in `-dry-run` as
  `[manual]`. **Run them by hand on a configured Mac** with
  `go run ./cmd/runevals -include-manual` (or `-only <id>` for a single one).
  None automates a send/call/print; mutations self-clean where an inverse exists
  (`m_notes_create_leaves_residue` is the exception, and says so). The system
  back-channel routing checks (`m_system_notify_banner`, `m_system_speak_aloud`)
  are manual because `notify`/`speak` are auto_commit — invoking the tool commits
  the side effect at once (a real banner, real audio), so there is no CI-safe
  selection-only call the way a staged mutation has. The window-management checks
  (`m_app_list_windows_readonly`, `m_app_move_window_then_undo`,
  `m_app_minimize_window`) are manual for the same reason plus a second grant:
  they need Accessibility access, and the move/minimize ops auto_commit against a
  real on-screen window (`m_app_move_window_then_undo` then undoes the move).
