**note**
Design choices for `spotlight_search` (U4 of the capability roadmap): a
general-purpose Spotlight lookup over `mdfind`, exposed as a read-only
filesystem operation.

- **Why a new op rather than lean on `find`/`grep`.** `find` matches on
  filename/extension/type and `grep` scans file contents line by line inside
  paths you already name. Neither consults Spotlight's index, which is what
  answers "find the presentation about Q3" — a query about a document's
  meaning/contents across the whole home directory in one cheap call. The tool
  description steers the model explicitly: Spotlight for topic/content
  questions, `find` for exact-name matches, `grep` for scanning known files.

- **Builtin, not the generic argv builder — and why that matters for
  security.** `mdfind` has NO `--` end-of-options terminator (it rejects `--`
  outright), so the generic builder's structural dash-guard cannot protect it.
  The guard therefore lives in the builtin, exactly as `search_mail` already
  established: a query beginning with `-` is refused up front
  (darwin-execution.md §4), because `mdfind` would otherwise read it as one of
  its own options (`-name`, `-onlyin`, …). Only the first character matters —
  once the argument starts with a non-dash, `mdfind` treats the whole thing as
  the query, so an interior dash ("foo -bar") is already safe.

- **The optional scope directory is neutralized by absolutization, not a
  dash-reject.** When `dir` is supplied it is resolved with `filepath.Abs`
  before being passed after `-onlyin`. An absolute path always begins with
  `/`, so a model-supplied value can never reach `mdfind` as a dash-leading
  token — the leading dash is made structurally impossible rather than merely
  rejected. The resolved directory is then `stat`ed so a nonexistent or
  non-directory scope reports a clean error instead of silently matching
  nothing (mirroring `largest_files`). Both guards are recorded in the one
  `reviewedFreeTextBuiltins` entry, which the injection-sweep coverage gate
  requires for any free-text builtin.

- **Output is bounded in Go.** Builtin output bypasses the subprocess
  truncation budget, so `spotlight_search` caps results itself (default 30,
  max 200) and its footer is honest about how many matches were found versus
  shown, nudging the caller to narrow the query or set a scope directory.

- **Evals are routing-only, by necessity.** Two selection cases in
  `filesystem_reads.json` ("find the presentation about Q3 planning",
  "search my Documents for the annual budget") assert the model reaches for
  `spotlight_search` (not `find`, not a pipeline) and that the call succeeds.
  Nothing about the returned paths is asserted: real matches depend on the
  running machine's Spotlight index, which is why deeper assertions would be
  flaky. The unit test covers the guards and formatting deterministically; the
  one real `mdfind` call is scoped to an empty temp dir so it is guaranteed to
  match nothing (see the Safety note in docs/TESTS.md).
