**bug**
`filesystem.remove` can silently fail to trash items when several removals of
same-named items are staged before any is executed, AND the engine reports the
failed move as "Applied" with a valid-looking undo token.

Two defects combine here:

1. **Trash destination is chosen at stage time, not execute time.**
   `trashPathFor` (`internal/engine/mutate_filesystem.go`) picks a
   collision-free name by checking whether `~/.Trash/<base>` exists *right now*.
   When N removals of same-named items (e.g. eighteen `.venv` folders) are
   staged in one batch, none has executed yet, so every stage sees an empty
   Trash and every plan gets the identical destination `~/.Trash/.venv`. At
   execute time the first `mv` succeeds; the second lands *inside* the first
   (`~/.Trash/.venv/.venv`); the third and later fail with
   `mv: rename ... to /Users/<user>/.Trash/.venv/.venv: Directory not empty`.
   The same applies to `copy`/`write_file`/etc. undo paths that call
   `trashPathFor`. The suffix logic ("`.venv 2`") never gets a chance to run.

2. **A non-zero `mv` exit is reported as success.** For the failed executions
   the tool returned `[stderr] mv: ... Directory not empty` and `[exit code: 1]`
   but then still printed `Applied "remove". To reverse it, call the undo tool
   with undo_token "..."`. A caller skimming the result (or an automated one)
   would believe the folder was removed and that undo is meaningful. Undoing
   such a token would try to move `~/.Trash/.venv` back over the source path,
   which is the wrong item and could clobber or misplace data.

**Observed** (2026-09-20, cleaning `.venv` folders under
`~/gh/kinnoo/agents-scratch`): 18 removals staged in parallel, 18 executed.
Only 2 actually moved (one to `~/.Trash/.venv`, one nested inside it); 16 failed
with exit 1 while still reporting "Applied". No data was lost (failed moves leave
the source untouched), but the sources were still on disk after "success".

**Workaround used:** rename each source to a unique name with `move`
(`venv-trash-N`), then `remove` it, so every Trash basename is distinct.

**Suggested fix:**
- Resolve the Trash destination at *execute* time (or re-check and re-pick when
  the staged destination now exists), and record the destination actually used
  so the inverse targets the right item. Alternatively reserve the chosen name
  in a per-process set at stage time so concurrent stages cannot pick the same
  one.
- Treat a non-zero exit from the forward command as a failed execution: do not
  print "Applied", do not mint an undo token (or mark it unusable), and surface
  the error as the result.
- Regression tests: (a) stage two `remove`s of same-basename paths before
  executing either and assert both end up in the Trash and both undo correctly;
  (b) a forward command that exits non-zero yields an error result and no undo
  token.

**Not yet verified:** whether the "Applied despite non-zero exit" behaviour is
specific to `mv`/`remove` or applies to every staged operation's execute path.
Check the shared execute path in `internal/engine/` before scoping the fix.

**fixed**
