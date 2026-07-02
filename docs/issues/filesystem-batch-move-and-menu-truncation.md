**note**

Three changes landed together after a plain "move all the screenshots on my
Desktop into ~/Desktop/screenshots" request failed end to end. The root causes
were structural, not a model mistake:

1. **The domain tool description was being truncated, hiding operations.** Each
   domain tool advertises its whole operation menu inside its description. The
   `filesystem` menu (15 operations, every parameter documented) is ~6 KB, and the
   tool-discovery layer clipped it alphabetically right after `grep` — so `move`,
   `mkdir`, and `remove` were invisible and the model couldn't form the call.

   Fix (`internal/server/menu.go`, applies to **every** category): the
   description now leads with a single compact `All operations: a, b, c, …` line
   naming every operation, *before* the detailed per-operation section. Even if a
   client clips the detail, that line survives and names every operation — and
   the mutators that sort to the end of the alphabet are no longer the first to
   vanish. Parameter rendering was also compacted (`name* (type): desc`, where
   `*` = required, replacing `, required`) to keep the menu small.

2. **There was no way to move many files by pattern.** `move` took exactly one
   literal `source`, so "move all screenshots" decomposed into one `find` plus N
   individual stage→execute pairs.

   Fix (`internal/engine/mutate_filesystem.go`): `move` now also accepts a
   `source_glob`. The **server** expands the pattern on disk and stages the whole
   batch as ONE reversible pair of commands:
   `Forward: mv -- <m1> <m2> … <destDir>` and
   `Inverse: mv -- <destDir>/<base1> … <commonParent>`. Because `mv` moves many
   sources into a trailing directory in one process, there is no partial-completion
   state for undo to reason about. Three invariants keep the single inverse correct
   and non-destructive: every match must share ONE parent directory (anchors the
   inverse; a multi-directory glob is rejected), the destination must be an
   EXISTING directory distinct from that parent, and no match's basename may
   already exist at the destination (no clobber). A `maxGlobMatches` cap (1000)
   bounds the blast radius of one approval.

3. **The model can't always reproduce an exact filename.** macOS screenshot names
   contain a narrow no-break space (U+202F) before AM/PM that collapses to an
   ordinary space when retyped, so a literal `source` typed back from a listing
   never matches on disk. Server-side glob expansion (#2) is the real fix — the
   model supplies a pattern, never the exact bytes. As a complement, when a single
   `source` misses, `validateExistingOperand` now scans the parent directory for a
   sibling that differs only in whitespace and, if found, returns an error that
   points at the real file and steers the caller to `source_glob`
   (`suggestWhitespaceMatch`/`normalizeWhitespace`).
