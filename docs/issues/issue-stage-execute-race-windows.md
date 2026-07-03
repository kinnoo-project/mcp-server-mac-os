**issue**
Two narrow time-of-check/time-of-use windows exist between the moment a filesystem
mutation is staged and the moment the user confirms and it executes. Both were
surfaced by the independent pre-merge review of PR #53 (see
`docs/pr-reviews/pr53.md`, findings M4 and L7). Neither is a defect in what the
commands do — they are consequences of the two-phase design proving its
preconditions at *stage* time and replaying the staged command verbatim at
*execute* time.

1. **Destination-clobber window (`write_file`, `move`, `copy`).** Staging proves
   the destination does not exist, but nothing re-checks at execute time. If
   something else creates that same path between stage and confirm, execute
   overwrites it: `tee` truncates an existing file, `mv`/`cp -R` replace the
   target. Undo then trashes the result as if this operation had created it, so
   the interloping file is recoverable from the Trash — but the staged preview's
   promise ("destination does not exist") was silently stale.

2. **Trash-name collision across concurrently staged plans (`remove`, and the
   copy/compress/extract inverses).** `trashPathFor` computes a collision-free
   `~/.Trash` name at stage time. Two plans staged before either executes can
   compute the SAME name for the same basename; when the second executes, its
   `mv` lands inside the first trashed item (if it was a directory) or replaces
   it.

Why not "just fix it": the obvious `mv -n` / `cp -n` no-clobber flags exit 0
even when they skip the operation, so execute would report success while doing
nothing — and the recorded inverse would then "undo" a move that never happened,
relocating the wrong file. A correct fix needs an engine-level execute-time
re-probe hook (re-verify the staged preconditions immediately before running the
forward command, refuse with a clear "state changed since staging, please
re-stage" error otherwise), which also naturally closes the trash-name race by
recomputing the trash destination at execute time. That is a cross-cutting
engine change, deliberately deferred rather than half-done inline.

Exposure in practice is low: stage→confirm gaps are seconds long, the paths
involved are user-named (not attacker-chosen temp names), and every clobbered
item still lands in the Trash rather than being destroyed.

**fixed**
