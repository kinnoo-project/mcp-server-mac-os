**note**

V6 — `security` domain, part 2: keychain metadata reads.

Three read-only operations added to the existing `security` domain (introduced in
V5). No new domain and no new MCP tool — these extend the `security` tool's
operation menu, so the tool surface stays at 23. Part of the Phase-2 "V units"
roadmap (`docs/ideas/capability-expansion-phase2.md`, V6).

## What each does

- **`find_credential`** (`security find-generic-password`) — does the keychain
  hold a saved app/service password, and if so under what account? Takes an
  optional `service` and/or `account` (at least one required) and reports ONLY
  the item's non-secret metadata: service, account, label, kind/description, and
  the created/modified dates.
- **`find_internet_credential`** (`security find-internet-password`) — the
  website/server-login twin, keyed by `server` and/or `account`; additionally can
  surface protocol/port/path when present.
- **`list_keychains`** (`security list-keychains`) — the keychain files on the
  search list (typically the login and system keychains). Fixed argv, no input.

A non-zero exit from the find-* forms (`errSecItemNotFound`) is the ordinary "you
have no saved password for this" answer and is reported as a plain result, not an
error.

## The one property that matters: secrets never leave the keychain

The macOS `security` tool CAN print a stored password — but only when asked with
`-w` (password to stdout) or `-g` (password to stderr). The design makes emitting
either flag impossible, and defends the property in depth:

1. **Argv pinning.** The argv builders (`findGenericPasswordArgs`,
   `findInternetPasswordArgs`, `listKeychainsArgs`) request attributes only; `-w`
   and `-g` never appear. This is asserted by
   `engine.TestSecurity_ConstrainedBinaryVerbs`, which now treats `security` like
   the other verb-pinned binaries: allowed first-args are the three sub-commands,
   and `-w`/`-g`/`-d`/`dump-keychain` are forbidden tokens anywhere in argv. So a
   future edit that added a secret-printing flag fails the build.
2. **Output allowlist.** `keychainMetadata` parses the attribute dump and re-emits
   ONLY a curated allowlist of attribute codes (`svce`, `acct`, `srvr`, `ptcl`,
   `port`, `path`, `desc`, `cdat`, `mdat`, plus the `0x00000007` label). Anything
   unrecognized — including app-defined blobs like `gena` that could carry
   sensitive data — is dropped, not forwarded. This is a second, independent layer:
   even if a future macOS surfaced secret-bearing data in the attribute dump, an
   un-allowlisted key can never reach the model.

`TestKeychainMetadata_SecretSafeAllowlist` proves layer 2 against a canned dump
that deliberately includes a secret-looking blob and undisclosed attributes.

## Injection posture

`find_credential`/`find_internet_credential` take model-controlled
`service`/`account`/`server` strings. Each rides as the VALUE of a flag
(`-s <value>`, `-a <value>`), so getopt binds it to that flag even if it begins
with `-` — option injection is structurally impossible. As defense in depth,
`validateKeychainQuery` still rejects empty, over-long, dash-leading, and
control-character values (the last also prevents a value smuggling a newline into
the rendered report). `list_keychains` takes no input. Registered in
`reviewedFreeTextBuiltins` with per-op `-e`/dash regressions in
`builtins_keychain_test.go`.

## TCC / permissions

Reading keychain metadata for items the calling process does not own can trigger a
keychain access prompt ("… wants to use your confidential information stored in
… in your keychain"). This is macOS's own gate and is expected; it is documented
in the README so the behavior isn't surprising. No Full Disk Access or Automation
grant is needed for the search-list read or for items the process already owns.

## Design choices / deferrals

- **Metadata only, by owner decision.** No write path and no password retrieval,
  ever — the `-w`/`-g` flags are structurally unreachable (owner decision #1 in the
  Phase-2 plan; matches the "exclude low-value / high-blast-radius verbs"
  precedent). `add-generic-password`/`delete-*` are out of scope.
- **`desc` labeled "Kind."** The `desc` attribute is what Keychain Access shows as
  the item's "Kind" (e.g. "AirPort network password"), so it is surfaced under that
  familiar label rather than "Description."
- **Timestamps left in compact UTC form** (`YYYYMMDDhhmmssZ`) after stripping the
  trailing NUL escape; it is unambiguous and avoids a brittle parse of the hex blob.

## Manual smoke test (to run on-device)

`MCP_SECURITY_LIVE=1 go test ./internal/engine -run TestKeychainBuiltins_Live`
exercises the real `security` binary: `list_keychains` returns the search list,
and a `find_credential` lookup for a common service is asserted never to contain a
`password:` line. The eval `security_keychain.json` also carries a manual case
(`m_find_credential_never_reveals_secret`) for a human to eyeball that a real
lookup's output shows account/label/dates but never the password value.
