**bug**
The `filesystem` `extract` operation refused plain, uncompressed `.tar` archives. It
accepted only `.zip`, `.tar.gz`, and `.tgz`, so a perfectly ordinary download such as
`~/Downloads/condvar.tar` was rejected at stage time with:

```
extract: archive "…/condvar.tar" must end in one of .zip, .tar.gz, .tgz
```

The underlying `bsdtar` reads a plain tar natively and needs no extra flag — the only
thing standing in the way was the server's own suffix allowlist. The practical cost was
that the user had to drop out to a raw shell to unpack the file, which is exactly the
escape hatch this server exists to make unnecessary.

Root cause: `compress` and `extract` shared one allowlist (`allowedArchiveExtensions`).
That list is correct for creation — we never want the server to hand back the bulkier
uncompressed variant — but creation and extraction do not have the same requirements.
Archives already on disk were not necessarily produced by this server, so extraction has
to accept formats we would not choose to write.

**fixed**
Split the single list into two directional allowlists in
`internal/engine/mutate_filesystem.go`, and added `.tar` to the read side only:

- `creatableArchiveExtensions` = `.tar.gz`, `.tgz`, `.zip` — what `compress` may create
  (unchanged; compression stays mandatory for archives the server produces).
- `extractableArchiveExtensions` = the above plus `.tar` — what `extract` may unpack.

`archiveExtension` now takes the set to match against, so each call site states which
direction it is going. `.tar.gz` stays ordered ahead of `.tar` so a gzip tarball reports
`.tar.gz` and is never misread as a plain tar.

The asymmetry is deliberate. Reading a format the world hands you is strictly safer than
choosing to write one, and nothing about the safety story is format-dependent: the
forward command is the same `tar -x -f <archive> -C <dest>`, run in bsdtar's secure
default (no `-P`), so the zip-slip guarantees — a `..` member refused, an absolute member
stripped of its leading `/` — and the empty-destination precondition that makes undo a
clean whole-directory recycle both apply unchanged.

Tests (`internal/engine/mutate_filesystem_test.go`):
- `TestStageExtract_PlainTarRoundTrip` — the regression, built on the real reported shape:
  an uncompressed tar stages with argv **identical** to the gzip case and really unpacks.
- `TestStageExtract_UppercaseTar` — `.TAR` matches, like every other suffix.
- `TestArchiveExtension` — rewritten as a table over both sets, pinning that a plain
  `.tar` is extractable but NOT creatable, that both sets stay closed, and that
  `backup.tar.gz` reports `.tar.gz` rather than `.tar`.
- Guardrail tables extended: `compress` rejects `out.tar`; `extract` rejects `ok.tar.bz2`
  (a real regular file, so only the extension check can be what refuses it).

Verified end-to-end against the actual `~/Downloads/condvar.tar` from the report: it now
stages and extracts, unpacking its `condvar/` directory.
