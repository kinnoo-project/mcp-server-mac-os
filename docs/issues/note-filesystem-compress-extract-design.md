**note**

Unit 9 of the capability-expansion roadmap (`~/.claude/plans/woolly-noodling-stream.md`)
adds two capabilities to the existing `filesystem` domain (no new MCP tool):
`compress` (create a `.zip`/`.tar.gz`/`.tgz` archive from one or more
files/folders) and `extract` (unpack such an archive into an empty directory).
Both are reversible-via-Trash mutators staged behind the `execute` token gate
(`compensatable` / `medium` risk).

Design choices worth recording:

- **bsdtar, never `ditto`.** `ditto` is on the security deny-list
  (`security_invariants_test.go`), so it can never back a capability. `/usr/bin/tar`
  on macOS is bsdtar (libarchive): it resolves under a trusted system dir, is not
  deny-listed, writes Zip as well as gzip-tar, and — the reason it is the right
  tool — refuses unsafe archive members by default. This is why the plan chose tar
  over `ditto`/`zip`/`unzip`.

- **The zip-slip guard is bsdtar's own, and we deliberately keep it.** Run without
  the `-P` flag, bsdtar stays in its secure default on extract: it refuses any
  member whose path contains a `..` component and strips a leading `/` from an
  absolute member so it lands *inside* the destination. `extract` therefore never
  passes `-P` and never assembles member paths itself — the archive is the only
  source of member names, and libarchive polices them. `TestStageExtract_RefusesZipSlip`
  crafts an archive with a `../escape.txt` member and a `/tmp/abs_evil.txt` member
  and asserts on the resulting disk state that neither escaped the destination.

- **`compress` uses `-C <parent> -- <basenames>` so members are clean and
  relative.** All sources must share one parent directory (the same shared-parent
  invariant the batch `move` enforces). The forward command changes into that
  parent with `-C` and passes each source by basename, so the archive stores
  `project/report.txt`, never a machine-specific `/Users/…` absolute path. The
  archive path itself is absolute, so `-f` is unaffected by the `-C` chdir. A `--`
  terminator precedes the basenames so a name beginning with `-` is still a file
  operand, never a tar flag. Basenames are sorted for a deterministic argv.

- **The format is fixed by the filename extension, from a closed allowlist.**
  `archiveExtension` accepts exactly `.zip`, `.tar.gz`, and `.tgz`
  (case-insensitively) and rejects everything else, up front, for both ops.
  bsdtar's `-a` flag picks the create format from that same suffix, so validating
  the suffix validates the format — there is no separate format parameter to keep
  in sync, and the model cannot smuggle a surprising binary format past the
  no-clobber / empty-dir guards.

- **Reversibility is Trash-recycling, made safe by strict preconditions.**
  Per the recycling rule (`transactional-state.md` §3), neither op hard-deletes.
  `compress`'s inverse moves the freshly-created archive to `~/.Trash` — safe
  because staging proved the archive path did not exist beforehand, so undo can
  only ever trash the file this op produced. `extract`'s inverse moves the *whole
  destination directory* to the Trash — safe because staging proved the
  destination was **empty** beforehand, so everything inside it afterward was
  unpacked by this op and nothing pre-existing is lost. That empty-directory
  precondition is exactly what lets the inverse be a single `mv`, keeping the
  forward a single `tar -x` (no need to enumerate and reverse individual members).

- **`extract` requires a pre-existing empty destination rather than creating one.**
  A mutator must not change state at stage time, and a staged plan is a single
  `Command`, so the mutator cannot both `mkdir` the destination and run `tar` in
  one forward step. Requiring the caller to create the (empty) directory first
  keeps the forward a lone `tar -x -C <dest>` and the inverse a lone `mv <dest>`.
  The tool description tells the model to create the folder with `mkdir` first; the
  `compress_extract_roundtrip` eval exercises that full compress→mkdir→extract flow.

- **Self-inclusion guard on `compress`.** An archive written at or under a
  directory being archived would make bsdtar try to capture the archive as it
  grows. `stageCompress` rejects an archive path that lies within any source tree
  (`isWithinDir`).

- **Injection: these are mutators, not builtins**, so they are covered by the
  mutator half of the injection story, not the `reviewedFreeTextBuiltins`
  coverage gate (that gate only enumerates in-process builtins). Every operand is
  dash-guarded and, where tar could parse it, sits after a `--`; the archive/dest
  paths are resolved to absolute form so any inverse is cwd-stable.

Bounds: `sources` is capped at `maxGlobMatches` (1000) items per archive; archive
size is not separately capped — a runaway archive is bounded by the executor's
2-minute kill, matching the rest of the filesystem domain.
