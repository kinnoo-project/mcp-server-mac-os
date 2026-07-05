**note**

# V2 — filesystem domain: media & document conversion

Phase-2 unit V2 adds five operations to the existing `filesystem` domain (no new
domain, no server changes — `filesystem` already exists, and `sips`/`textutil`/
`qlmanage` all resolve under `/usr/bin`, so the policy layer needs no change).
One is a read-only builtin; three are staged reversible mutators; one is a benign
auto-commit reversible mutator.

| Op | Kind | Binary | Notes |
|---|---|---|---|
| `image_info` | RO builtin | `sips` | `--getProperty all`; pixel dimensions / format / color space / DPI / byte size; complements the generic `file`/`stat` (which report only the on-disk type and size, not pixel dimensions) |
| `convert_image` | staged / medium / compensatable | `sips` | `-s format <enum: jpeg\|png\|tiff\|heic\|pdf> <src> --out <dst>`; writes a NEW file; inverse trashes it |
| `resize_image` | staged / medium / compensatable | `sips` | exactly one of `width`/`height`/`max_dimension` → `--resampleWidth`/`--resampleHeight`/`--resampleHeightWidthMax`; `--out` mandatory (see below); inverse trashes it |
| `convert_document` | staged / medium / compensatable | `textutil` | `-convert <enum: txt\|html\|rtf\|rtfd\|docx\|odt> -output <dst> -- <src>`; writes a NEW file; inverse trashes it |
| `quicklook_thumbnail` | auto-commit / low / reversible | `qlmanage` | `-t -s <size 1–2048> -o <scratch dir> -- <path>`; preview PNG into a server temp folder; inverse trashes the folder |

## Why `resize_image` forces `--out`

`sips` resamples **in place** when `--out` is omitted, which would irreversibly
mutate the caller's original image. The mutator therefore always supplies `--out`
pointing at a brand-new destination and never touches the source — which is also
what makes the "undo = trash the created file" inverse safe.

## Why `quicklook_thumbnail` is auto-commit and writes a scratch dir at stage time

Generating a preview is benign and frequent ("preview this file"), low-risk, and
reversible, so it runs immediately (registry-enforced none/low risk) rather than
behind the execute-token gate, handing back an undo token. `qlmanage` requires
its `-o` output directory to already exist and names the output file
`<input-basename>.png` inside it, so staging performs one benign server-owned
write: it creates a fresh unique directory under `/tmp/mcp-fallback` (via
`os.MkdirTemp`). This is the same documented "staging performs one side effect"
exception `print_test_page` already relies on — it touches only a temp directory
the server owns, never user or system state. The per-invocation directory gives
undo a precise target (the whole directory is this operation's product), so the
inverse moves it to the Trash rather than hard-deleting it.

## Injection posture

`sips` has **no** usable `--` end-of-options terminator — verified on-device: it
parses a literal `--` as an unknown function and aborts. So for every `sips` path
the defense is up-front validation: `validateExistingOperand` (source/inspected
path) and `validateNewOutputPath` (destination) both reject a dash-leading value
and resolve the path to **absolute** form (so it then begins with `/` and can
never be read as a `sips` option) before any argv is assembled. `textutil` and
`qlmanage` *do* honour `--`, so their source path additionally rides after a `--`
terminator as defense in depth — while still being dash-rejected and resolved
absolute like every other operand. Every `format` is a closed enum the registry
validates before the mutator runs, so it carries no injection surface. The new
builtin is recorded in `reviewedFreeTextBuiltins` and the three mutators in
`reviewedFreeTextMutators`; `media_filesystem_test.go` carries the per-op
dash-leading regressions and `TestConvertImage_RejectsBadFormatViaEngine` proves
the enum guard end-to-end (CLAUDE.md §4).

## No-overwrite discipline

The three converters each write a brand-new destination and refuse a path that
already exists (`validateNewOutputPath` uses `os.Lstat`, so even a dangling
symlink counts as occupied), and require the destination's parent directory to
exist. That is what keeps the Trash inverse non-destructive: undo can only ever
trash the file this operation created, never pre-existing user data — the same
property `write_file`/`compress` rely on.

## Scope note

`textutil` does **not** produce PDF (its formats are txt/html/rtf/rtfd/docx/odt),
so `convert_document`'s enum omits it; the tool description points a PDF request
at `convert_image` (sips can emit PDF from an image) or printing to PDF.

## Eval / verification note

Unit tests (routing-independent: argv pinning, new-path enforcement, the
dash-leading + bad-enum regressions, the resize dimension/clamp logic, and real
`sips`/`textutil`/`qlmanage` round trips) are green in-package. The three new
eval cases in `evals/cases/filesystem_media.json` (convert_image / image_info
selection + a convert_document stage→execute→undo, which is CI-safe because
`textutil` converts an empty scratch `.txt` with no image fixture needed) load
cleanly and were confirmed via the runevals dry-run. Fresh-server routing and
live execution of the new ops could **not** be exercised in-session: the MCP
server attached to the authoring session is the pre-build binary and still lists
only the original filesystem ops, so the new operations are only reachable from a
rebuilt server (the `go run ./cmd/runevals` / CI path). That fresh-server routing
+ live pass is the pending manual confirmation for this unit.
