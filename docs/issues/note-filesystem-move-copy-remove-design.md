**note**

Added three reversible mutating filesystem operations — `move`, `copy`, and
`remove` — alongside the existing `mkdir`. They follow the established mutator
pattern: a JSON manifest entry (`reversibility: reversible`, `risk: medium`,
`builder: <name>`) plus a small named `Mutator` registered in `mutate.go`. No
new Go execution machinery and no policy change were needed — the binary
allowlist is directory-based (`/bin`, `/usr/bin`, …), so `mv` and `cp` already
resolve.

Design decisions:

- **Everything reduces to `mv`/`cp` plus the Trash; no `rm` anywhere.** This
  keeps every operation reversible and honours the recycling rule in
  `.claude/rules/transactional-state.md` §3 ("never call final destructive
  purging actions directly").
  - `move`: forward `mv -- <src> <finalDest>`, inverse `mv -- <finalDest> <src>`.
  - `copy`: forward `cp -R -- <src> <finalDest>`, inverse moves the
    freshly-created copy into the user's Trash (so undo is recoverable, not a
    hard delete).
  - `remove`: forward `mv -- <src> <trash>` (recycle to `~/.Trash`), inverse
    `mv -- <trash> <src>`. "Delete" is therefore a move, never an `rm`; the user
    has two restore paths (the engine's undo token and Finder's "Put Back").

- **"Move/copy X into folder Y" semantics.** When `destination` is an existing
  directory, the final path is `destination/basename(source)` (matching native
  `mv`/`cp` and the user's mental model). This is the exact scenario from the
  bug report ("move test.txt to the Desktop folder" → `~/Desktop/test.txt`).

- **No-clobber invariant.** Staging refuses when the computed destination
  already exists. This both prevents silent overwrites and guarantees the
  inverse can restore the prior state exactly (we never overwrite something we
  cannot bring back) — the same conservative stance `stageMkdir` takes.

- **Absolute-path resolution + dash-leading guard.** User paths are made
  absolute at stage time so the inverse is stable regardless of the working
  directory when `undo` later runs, and a leading `-` is rejected up front
  (mirroring `mkdir`/`find`) even though the `--` terminator already protects
  later operands — belt-and-suspenders, with a clear "prefix with ./" message.

- **Trash collision handling.** `trashPathFor` appends a numeric suffix
  (`test 2.txt`) when a same-named item already sits in the Trash, mirroring
  Finder, so an existing trashed item is never overwritten.

Tests live in `internal/engine/mutate_filesystem_test.go`; the Trash-routed
round trips redirect `$HOME` to a temp dir so the real Trash is never touched.

Separately, the subprocess output-compaction budget (`maxOutputBytes` in
`internal/engine/executor.go`) was raised from 8 KB to 32 KB. The old cap was
tight enough that ordinary listings (e.g. a manifest dump) lost their middle to
truncation; 32 KB keeps everyday output intact while still guarding against a
runaway multi-megabyte dump. `.claude/rules/darwin-execution.md` §2 was updated
to match (16 KB head + 16 KB tail).
