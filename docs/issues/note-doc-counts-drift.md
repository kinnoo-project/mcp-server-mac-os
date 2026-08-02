**note**

The narrative docs drifted badly behind the registry. As of 2026-08-01,
`docs/architecture.md` still claimed **10 domain tools / ~47 operations / an 8 KB
output cap** and documented a capability catalog covering only 10 of the 23
domains, while the manifests actually held **174 capabilities across 23
domains** and `executor.go`'s budget had been raised to **32 KB**. `README.md`
still said "15-domain tool surface" and "18 eval cases"; `docs/TESTS.md` still
said "nineteen domain tools (22 tools total)".

Root cause: every one of those numbers is derivable from data (the manifests,
`maxOutputBytes`, `evals/cases/*.json`), but each was hand-written into prose, so
each capability PR silently invalidated a handful of sentences nobody was
prompted to touch. The drift compounds — by the time a doc is a dozen domains
stale, nobody trusts or updates it at all.

**How to regenerate the counts** rather than hand-tallying them (this is what
produced the corrected figures):

```bash
# domains, ops, and the read/mutate split per domain
python3 -c "
import json,glob,collections
d=collections.defaultdict(list)
for f in glob.glob('internal/registry/manifests/*.json'):
    for c in json.load(open(f)): d[c['category']].append(c)
for k in sorted(d):
    ro=sum(1 for c in d[k] if c['reversibility']=='read_only')
    print(f'{k:24} {len(d[k]):3} total  {ro:3} read  {len(d[k])-ro:3} mutate')
print(len(d),'domains', sum(len(v) for v in d.values()),'ops')"

# reversibility mix + auto-commit count
# builders: grep the three maps in engine/argbuild.go, builtins.go, mutate.go
# eval corpus: count objects in evals/cases/*.json, and those with "manual": true
```

Derived facts worth keeping in one place, since several docs repeat them:
174 operations · 23 domains · 26 MCP tools (23 + `execute`/`undo`/`pipeline`) ·
104 read-only / 70 mutating (25 reversible, 12 compensatable, 33 irreversible;
21 auto-commit) · 4 argv builders (generic + `find`/`grep`) · 94 builtins ·
70 mutators · 65 distinct native binaries · 32 KB output cap · 167 eval cases
(114 automated, 53 manual) · 587 test functions in 85 files.

**Follow-up worth considering:** a `go test` invariant that parses the counts out
of `README.md` / `docs/architecture.md` and fails when they disagree with the
registry — the same trick already used to keep the `write_setting` manifest enum
and its Go allowlist in sync (`TestDefaultsAllowlist_MatchesManifestEnum`) and to
keep the screenshot format enum honest. That would convert this class of drift
from "someone notices a year later" into a build failure.
