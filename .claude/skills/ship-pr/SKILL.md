---
name: ship-pr
description: End-to-end autopilot from an approved plan — branch, build+test+commit incrementally, push, open a PR to build, request Copilot, wait for Copilot's review, address its comments, then notify you it's ready to merge. Invoke once after approving a /plan; runs hands-off until the ready-to-merge notification.
---

# ship-pr — plan-to-mergeable autopilot

Triggered as `/ship-pr` (optionally `/ship-pr <base-branch>`; base defaults to
`build`). Run it **once** right after you have approved an implementation plan
(typically produced by `/plan`). From that point it runs WITHOUT further
intervention until it notifies you the PR is ready to merge. It never merges —
the human keeps the merge gate.

The whole thing is one skill invocation spanning two wall-clock phases. Phase A
(branch → build → PR) runs synchronously. Then a background watcher waits for
Copilot and, when Copilot's review lands, the harness re-invokes this same skill
to run Phase B (address comments → notify). You do nothing in between.

## Preconditions (check first; stop if unmet)

1. **An approved plan must be in context.** This skill implements an
   already-agreed plan — it does not invent scope. If the conversation has no
   approved plan (from `/plan` or an explicit plan the user signed off on), stop
   and say so rather than guessing what to build.
2. **Clean-ish working tree on a sane base.** If there are unrelated uncommitted
   changes, surface them and stop; don't sweep them into the feature branch.
3. Resolve `owner/repo` from the git remote (`gh repo view --json nameWithOwner`)
   and the base branch (arg, else `build`).

## Phase A — branch, build, push, open PR

Run these in order. Committing completed work without asking is pre-authorized
for this repo, so do not pause for per-commit confirmation.

1. **Branch.** Update the base branch and cut a feature branch from it:
   `git fetch origin && git switch <base> && git pull --ff-only`, then create the
   feature branch. Use these naming conventions:
   - **Plan implementations / new features**: `feat/<short-kebab-description>`
     (e.g. `feat/eval-layer-b-harness`, `feat/printer-capability`)
   - **Bug fixes or small corrections**: `fix/<short-kebab-description>`
     (e.g. `fix/move-dest-path`, `fix/whitespace-normalization`)
   Keep the slug to 3–5 words. Never build on the base branch itself.
2. **Implement incrementally.** Work the plan task by task (this is the `/build`
   auto pass, inline). After each coherent task:
   - run the project verify pipeline —
     `go build ./... && go vet ./... && [ -z "$(gofmt -l .)" ] && go test ./...`
     (add `-race` for concurrency packages like `internal/transaction`);
   - only when it is fully green, `git commit` that task with a message that
     says what changed and why. Never commit a red build.
   - If a step fails and you cannot fix it after a reasonable attempt, STOP and
     report — do not push or open a PR on top of a failure.
   Honor every CLAUDE.md and `.claude/rules/*` guardrail while implementing
   (stdout hygiene, argv-only exec + `--`/dash-leading input hardening, two-phase
   staging for mutations, self-documenting code, no real PII in tests).
3. **Push** the branch: `git push -u origin <feature-branch>`.
4. **Open the PR to the base branch** and request Copilot:
   ```bash
   gh pr create --base <base> --head <feature-branch> --fill
   gh pr edit <N> --add-reviewer Copilot   # falls back to the repo's
                                           # "auto-request Copilot review" setting
   ```
   Capture the PR number `N`. If `--add-reviewer Copilot` errors (org/settings
   dependent), don't fail the run — Copilot is usually auto-requested on PR
   open; the watcher below detects the review either way.
5. **Arm the Copilot watcher as a background job** (`Bash` with
   `run_in_background: true`). It polls the PR's reviews and **exits the instant
   Copilot submits one**, which makes the harness re-invoke this skill for Phase
   B. It is bounded so a silent Copilot can't hang forever:
   ```bash
   OWNER_REPO="<owner/repo>"; N=<N>
   deadline=$(( $(date +%s) + 2400 ))   # ~40 min cap
   while [ "$(date +%s)" -lt "$deadline" ]; do
     n=$(gh api "repos/$OWNER_REPO/pulls/$N/reviews" \
           --jq '[.[] | select(.user.login=="Copilot" or (.user.login|ascii_downcase|startswith("copilot")))] | length' \
           2>/dev/null || echo 0)
     if [ "${n:-0}" -ge 1 ]; then echo "copilot-review-ready pr=$N"; exit 0; fi
     sleep 30
   done
   echo "copilot-review-timeout pr=$N"; exit 0
   ```
   After arming it, end the turn. Tell the user Phase A is done and you're now
   waiting on Copilot for PR #N — nothing more for them to do.

## Phase B — address Copilot, then notify (runs on re-invocation)

When the watcher exits, the harness re-invokes this skill. Read the completion
line:

- **`copilot-review-timeout`** — Copilot didn't review within the window. Do NOT
  fabricate fixes. `PushNotification` the user that PR #N is open but Copilot
  hasn't reviewed yet, and stop.
- **`copilot-review-ready`** — proceed:
  1. **Address the comments by following the `pr-review` skill's Fix mode
     exactly** for PR #N: read each comment in context, fix (or reasoned-decline)
     in the working tree, add/adjust tests where warranted, run the full verify
     pipeline, commit, push to the PR branch, reply to each comment referencing
     the fix SHA, and write `docs/pr-reviews/pr<N>.md`. Do not re-derive that
     process here — defer to that skill so behavior stays consistent.
  2. **Notify.** Once replies are pushed, send a `PushNotification`:
     `"PR #<N> ready to merge — Copilot comments addressed in <sha>."` Keep the
     human as the one who merges.

## Notes & honest limits

- **One human gate, by design:** merge. Everything up to it is autonomous; the
  notification is the handoff.
- **Copilot may comment more than once.** This skill handles the first review
  pass. If Copilot re-reviews after your fixes and you want that handled too,
  re-run `/ship-pr` is overkill — instead re-arm just the watcher + Phase B, or
  invoke `/pr-review <N>` again. (A future enhancement could loop Phase B until a
  review pass yields zero new actionable comments.)
- **Requesting Copilot** depends on the repo/org having Copilot code review
  enabled; the most reliable setup is the repo setting that auto-requests Copilot
  on every PR, so step A4's explicit request is best-effort.
- **Never merges, never force-pushes shared history, never rewrites the base.**
  If the base branch has moved and the PR won't fast-forward cleanly, report it
  rather than resolving conflicts blindly.
