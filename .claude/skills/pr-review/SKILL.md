---
name: pr-review
description: Reviews a GitHub PR, or addresses existing review comments on one (e.g. from Copilot), depending on PR state and the user's prompt. Takes a PR URL or PR number as an argument.
---

# PR Review / PR Comment Resolution

Triggered as `/pr-review <PR URL or PR#>` (e.g. `/pr-review 5` or
`/pr-review https://github.com/<owner>/<repo>/pull/5`). Extract the PR
number from whichever form was given; `gh` resolves the repo from the local
git remote unless the URL points elsewhere.

## Step 1 — Decide the mode

Fetch the PR's existing review comments first, since the mode depends on
what's already there:

```bash
gh api repos/{owner}/{repo}/pulls/{N}/comments --jq '.[] | {id, path, line, user: .user.login, body}'
```

- If the user's prompt **explicitly** says what to do ("review PR 5",
  "address the comments on PR 5", "fix Copilot's feedback on #5"), follow
  that instruction directly — it overrides the heuristic below.
- Otherwise, decide from PR state: if there are existing review comments
  from another reviewer (a bot account like `copilot-pull-request-reviewer`,
  `Copilot`, or any author other than the current git user) that don't yet
  have a reply from this session, go to **Fix mode**. If there are none, go
  to **Review mode**.

## Step 2a — Fix mode (addressing existing comments)

For each unresolved comment, in order:

1. **Read it in context.** Open the file at the comment's line and understand
   the surrounding code before deciding anything — don't fix from the
   comment text alone.
2. **Decide if it's warranted.** Most automated review comments are correct
   and worth fixing; occasionally one is wrong, premature, or out of scope —
   it's fine to disagree, but say so explicitly with reasoning rather than
   silently ignoring it.
3. **Implement the fix** (or decide not to, with reasoning) directly in the
   working tree. Add or update tests that would have caught the issue where
   that makes sense, not just the minimal line change.
4. Record it for the local writeup (Step 3) using the same `(path) comment`
   format the user provides when pasting these in by hand:
   ```
   (internal/transaction/store.go) Store.Put never purges expired entries...
   ```

After all comments are triaged:

5. **Verify.** Run this project's standard pipeline before committing:
   `go build ./... && go vet ./... && [ -z "$(gofmt -l .)" ] && go test ./...`
   (add `-race` for packages with concurrency, e.g. `internal/transaction`).
   Fix anything broken before proceeding — never commit a red build.
6. **Commit and push** to the PR's branch (check out with
   `gh pr checkout {N}` first if not already on it). One commit covering all
   the comments addressed in this pass is fine; describe what changed and
   why per-comment in the body.
7. **Reply to each original comment on GitHub**, referencing the fix commit:
   ```bash
   gh api repos/{owner}/{repo}/pulls/comments/{comment_id}/replies -f body="Fixed in <sha> — <one or two sentence summary>."
   ```
   If a comment was deliberately not addressed, reply explaining why instead
   of fixing it silently.
8. **Write `docs/pr-reviews/pr{N}.md`** (see Step 3 for the format).

## Step 2b — Review mode (no existing comments to address)

1. Get the diff: `gh pr diff {N}` (or `gh pr view {N} --json files` plus
   reading each changed file for full context — the diff alone often isn't
   enough to judge correctness).
2. Review the changes across the same axes a thorough human reviewer would:
   correctness/bugs, readability, architectural fit with the rest of the
   codebase, security (this project's `.claude/rules/darwin-execution.md`
   and `transactional-state.md` guardrails in particular), and performance.
3. **Do NOT post comments to the GitHub PR.** This mode produces a local
   record only — `docs/pr-reviews/pr{N}.md` — for the user to read and decide
   what to act on. (If the user explicitly asks you to also post the review
   to GitHub in a given invocation, that's a one-off explicit request, not
   this skill's default.)
4. If a finding is small, obviously correct, and the user's invocation implied
   they want fixes too (not just a review), it's fine to fix it directly and
   note that in the writeup — otherwise leave findings unfixed for the user
   to triage, since "review" and "fix" are different asks.

## Step 3 — `docs/pr-reviews/pr{N}.md` format

Create the file if it doesn't exist; if it does (a second pass on the same
PR), append a new dated section rather than overwriting prior history.

```markdown
# PR #{N} review — {PR title}

{date}, mode: {fix|review}

(internal/transaction/store.go) Store.Put never purges expired entries from
the map. Because entries are only removed when their own token is Taken...

**fixed**
Put now opportunistically purges expired entries before inserting (see
purgeExpiredLocked). Added TestStore_PutPurgesExpiredEntries.

---

(internal/evals/anthropic.go) Anthropic API calls have no timeout/deadline...

**fixed**
sendMessage now wraps ctx with a 60s context.WithTimeout.
```

For review-mode findings with nothing fixed, replace `**fixed**` with
`**status**: open` and a one-line note on suggested next step instead.

## Notes

- This mirrors `docs/issues/`'s note/issue/bug convention (CLAUDE.md §8) but
  is scoped to one PR's comment thread rather than general project notes —
  keep PR-specific writeups here, not in `docs/issues/`.
- Never skip the verification pipeline before pushing a fix, even for a
  one-line change — a broken build is worse than an unaddressed comment.
- If `gh` reports the PR is already merged or closed, fix mode can still
  apply (e.g. addressing post-merge feedback on a follow-up branch), but
  confirm with the user before pushing anywhere, since the target branch may
  no longer be what they expect.
