# Shortcuts Runner — Deferred Design

> **Tabled pending owner decision — not in the active build order.** Preserved
> verbatim from the capability-expansion roadmap (2026-07-01) so it can be picked
> up later without re-derivation.

**new tool: `shortcuts` *(force multiplier — most careful security review)***

The sanctioned automation surface for everything with no clean CLI: Focus modes,
DND, HomeKit, user-defined flows. Also the project's riskiest op: a shortcut is
arbitrary automation.

- `list_shortcuts` — RO builtin, `shortcuts list --show-identifiers`, fixed argv.
  **RO/none.**
- `run_shortcut` — mutator; Forward `shortcuts run -- <name>` (ArgumentParser
  supports `--`; verify in unit test) **plus** dash-leading name rejection (belt
  and suspenders). Stage-time existence check against `shortcuts list` output so
  the preview names a real shortcut. Optional input restricted to an existing
  file path (dash-guarded). **irreversible / HIGH / ST**; preview names the
  shortcut verbatim and states there is no undo.
- **Pin `run_shortcut` into `dangerousOps` in `security_invariants_test.go`** so
  its high/irreversible/gated metadata can never regress.
- Evals: selection + `forbid_tools:["execute"]` A; benign fixture shortcut M.
  Document in the tool description that this is the Focus/DND/HomeKit path.

**Risk rationale for `run_shortcut`:** a shortcut is arbitrary automation
authored outside this server, so its blast radius is unbounded and there is no
meaningful inverse — hence HIGH risk, irreversible, staged behind the `execute`
token gate, and pinned in the security-invariants test. If it is ever built,
that classification must not be softened.
