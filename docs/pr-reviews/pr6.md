# PR #6 review — build

2026-06-18, mode: fix

(internal/engine/mutate_preferences.go) `probeDefaultsValue` treats *any* non-zero exit from `defaults read` as "unset" and drops stderr. That can misclassify real errors (e.g., malformed domain/path, permissions, other defaults failures) as a missing key and stage an unsafe/incorrect inverse. Consider only treating the well-known "does not exist" case as unset and returning an error for other failures (including stderr) so staging fails loudly.

**fixed**
`probeDefaultsValue` now only treats a non-zero exit as unset when stderr contains "does not exist"; any other failure returns as an error instead of silently staging an incorrect inverse. The caller (`stageWriteSetting`) already propagated this error correctly, so no other change was needed. Commit `0395df4`.
