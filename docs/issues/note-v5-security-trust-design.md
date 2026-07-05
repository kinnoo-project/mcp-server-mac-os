**note**

V5 — new `security` domain, part 1: app trust & integrity reads.

Four read-only operations in a brand-new `security` domain (category `security`,
so the MCP tool surface grows 22 → 23 with no server change). Part of the Phase-2
"V units" roadmap (`docs/ideas/capability-expansion-phase2.md`, V5) and the first
unit that both introduces a new domain AND edits the registry deny list.

## What each does

- **`verify_signature`** (codesign) — is an app/binary code-signed, and by whom?
  Runs `codesign --verify --deep --strict -dvvv <path>`, which both verifies the
  signature is intact and displays the signing authority chain and Team ID (to
  stderr). The exit code drives a plain-language verdict (VALID vs not).
- **`gatekeeper_check`** (spctl) — would Gatekeeper allow the app to run? Runs
  `spctl --assess --type exec -vv <path>`; exit 0 = accepted, non-zero = rejected,
  both surfaced as data with the matched source (Notarized Developer ID, etc.).
- **`sip_status`** (csrutil) — is System Integrity Protection enabled? Runs the
  fixed `csrutil status`. No model input.
- **`quarantine_info`** (xattr) — was a file downloaded from the internet? Reads
  the `com.apple.quarantine` attribute with `xattr -p`; a present value is decoded
  into the downloading app and timestamp, an absent one (non-zero exit) reported
  as "NOT quarantined".

## Design decisions

**Deny-list edit #1: csrutil and spctl come off, safety moves to verb pinning.**
Both were on the registry deny list (`security_invariants_test.go`). Reaching the
read-only probes requires removing them, so the "this binary is categorically
unreachable" guarantee is replaced with a narrower, equally-enforced one: each
constrained binary may be invoked with exactly ONE read-only verb. codesign and
xattr were never on the deny list, but they get the same treatment because they
are equally capable of mutation (codesign can sign, xattr can write attributes).

The pin lives in four tiny pure functions (`codesignVerifyArgs`,
`spctlAssessArgs`, `csrutilStatusArgs`, `xattrQuarantineArgs`) that assemble the
argv, and the new invariant `TestSecurity_ConstrainedBinaryVerbs`
(`engine/security_verbs_test.go`) asserts, per binary, that the argv starts with
an allowed verb and contains none of the state-changing tokens (spctl never
`--add`/`--enable`/`--master-disable`, csrutil never `disable`/`enable`, xattr
never `-w`/`-d`/`-c`, codesign never a signing verb). Keeping argv assembly in
those pure functions is what lets the test prove the pins without launching the
tools. This is the reusable frame V7 (storage: tmutil/diskutil/hdiutil) extends.

**Injection posture.** Three operations take a model-controlled path. codesign
and spctl have NO usable `--` end-of-options terminator, so the sole defense
there is `validateExistingOperand`, which rejects a dash-leading value and
resolves the path to ABSOLUTE form (so it starts with `/` and can never be read
as a flag). xattr DOES honour `--`, so its path additionally rides after a `--`
terminator as defense in depth. All three ops are registered in
`reviewedFreeTextBuiltins` with per-op hostile-path regressions
(`TestSecurity_HostilePathRejected` feeds `-e`, `-rf`, `--flood`, `-` and asserts
each is refused before any subprocess runs). sip_status takes no input.

**Metadata-only, read-only.** Every op is `read_only`/`risk: none` and never
changes a security setting. This matches the owner decision to keep the domain a
pure trust-inspection surface (and mirrors the "exclude low-value destructive
ops" precedent — no "disable SIP", no "clear quarantine" writes).

**No policy change needed.** All four binaries already resolve under the trusted
system directories (`/usr/bin/codesign`, `/usr/sbin/spctl`, `/usr/bin/csrutil`,
`/usr/bin/xattr`), confirmed by `TestSecurity_AllBinariesResolveToTrustedDirs`.

## Manual on-device verification

The pure helpers and guards are covered by unit tests. The end-to-end path
(`TestSecurityBuiltins_Live`, gated by `MCP_SECURITY_LIVE=1`) shells out to the
real codesign/spctl/csrutil/xattr against `/bin/ls` and should be run once
on-device. The three eval cases in `evals/cases/security_trust.json` verify
routing; they could not be exercised by an in-session live eval run because the
MCP server binary in this session predates the new `security` domain (same
situation as V4) — routing was verified from the prompts and execution from the
Go live test.
