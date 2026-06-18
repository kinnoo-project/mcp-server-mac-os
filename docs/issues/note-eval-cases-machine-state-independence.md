**note**

Audited all 14 eval cases (`evals/cases/*.json`) for dependence on the
specific Mac running them, in response to a direct question about it.

Findings:

1. **Every case's pass/fail logic checks which tool/operation was called, not
   the returned content.** None assert on file counts, line counts, specific
   file contents, or a setting's actual current value — so the eval itself
   isn't comparing against machine-specific data.

2. **Found and fixed a real fragility:** `file_identify_type` referenced
   `/usr/bin/python3`. Apple has removed bundled interpreters from `/usr/bin`
   in some macOS versions, so this case could spuriously fail on a machine
   where that path doesn't exist — not because the model chose the wrong
   tool, but because it might reasonably decline to call `file` on a path it
   has reason to doubt. Changed to `/bin/ls`, a core system binary guaranteed
   present on every macOS install. Re-ran live after the fix: still passes.

3. **`/etc/hosts` and `/etc`** (used in `wc_count_lines`, `grep_search_contents`,
   `stat_file_metadata`, `find_files_by_extension`) are core macOS files/dirs
   present by default on every install — effectively no dependency.

4. **`write_setting_*` cases genuinely read and write a real existing
   preference** (Finder's `AppleShowAllFiles`), then restore it via the scripted
   undo turn. This is intentional and was designed to be idempotent regardless
   of the prior value (unset, true, or false all round-trip correctly — see
   `internal/engine/mutate_preferences.go`'s `stageWriteSetting`). The one
   theoretical (and in practice never-seen) edge case: if that exact key
   already held a non-boolean value, staging would refuse, and the case would
   fail for an environmental reason rather than a model-selection one. Not
   considered worth engineering around given how this key is conventionally
   only ever set via `-bool`.

5. **`mkdir_*` cases use the `{{unique}}` placeholder** specifically so they
   never depend on (or collide with) anything left on disk from a prior run.

Net: the eval suite is state-independent for scoring purposes, with one
fragility found and fixed during this audit.
