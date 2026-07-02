**issue**

`move` now supports batch selection via `source_glob`, but `copy` and `remove` do
not — they still take a single literal `source`/`path`.

The reason is reversibility, not effort. `move`'s batch fits the engine's
single-`Command` `StagedPlan` cleanly: one `mv` moves every match into the
destination directory, and one `mv` moves them all back. `copy` and `remove`
cannot be expressed as one reversible command per batch:

- batch `copy`'s inverse must trash the N freshly-made copies, and each needs a
  collision-free Trash name (`trashPathFor`) — that is N separate moves, not one;
- batch `remove`'s forward must recycle N files to the Trash, again with N
  per-file collision-free names.

Doing either correctly requires generalizing `StagedPlan` to hold an ordered
*sequence* of forward/inverse commands and giving `execute`/`undo` partial-failure
semantics (if step k of N fails, what is rolled back and reported?). That is the
already-planned "multi-step mutation plans" work; batch `copy`/`remove` should be
built on top of it rather than hacked into the single-command shape.

Until then, multi-file copy/remove is N individual staged operations.

**fixed**

Not yet. Deferred to the multi-step mutation-plan work.
