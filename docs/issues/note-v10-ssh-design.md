**note**

V10 — SSH connect, added to the existing `network` domain (no new MCP tool):
`list_ssh_keys`, `list_ssh_hosts` (read-only), and `ssh_connect` (a staged,
medium-risk mutation that opens Terminal on a constructed `ssh` command). This is
the final unit of Phase 2 (V0–V10). Operations total 168 → 171; domains stay at
22.

## The three operations

- `list_ssh_keys` — **RO / low** builtin. Scans `~/.ssh`, and for every public
  (`.pub`) file runs `ssh-keygen -l -f <pub>` to report its type, fingerprint,
  comment, and whether the matching private key is present. **Private-key safe:**
  only the `.pub` half is ever read/fingerprinted; private bytes are never opened
  (presence is an `os.Stat`). This mirrors the keychain domain's "metadata only"
  posture.
- `list_ssh_hosts` — **RO / low** builtin. Pure in-process parse of
  `~/.ssh/config` (no subprocess, no key material) reporting each concrete Host
  block's alias, HostName, User, IdentityFile, and Port. Wildcard stanzas
  (`Host *`) are skipped — they are defaults, not addressable servers.
- `ssh_connect` — **ST / medium / irreversible** mutator. Opens Terminal.app on
  `ssh [-i <key>] [-p <port>] <user>@<host>`.

## Why ssh_connect hands off to Terminal instead of running ssh

An SSH session is interactive: first connect prompts for a host-key fingerprint,
then for a password or key passphrase. This server's non-interactive stdio
transport cannot answer those prompts, and — a hard rule — it must never see a
password. So `ssh_connect` does not run `ssh` at all. It stages an AppleScript
that opens a new Terminal window and types the constructed command there, where
the human completes authentication under their own control. The capability's
effect is therefore "a Terminal session was started," which has no inverse (you
close the window yourself) — hence irreversible and always staged for
confirmation. It is medium (not high) risk: no data is destroyed and nothing runs
on the remote until the user authenticates in the window they can see.

## The security crux: one string, two interpreters

The staged command string crosses **two** interpreters, and each needs its own
defense:

1. **osascript.** The command rides as a single DATA argument bound to the fixed
   script's `on run argv`, through the shared `osascriptCommand` seam, which
   inserts a `--` end-of-options terminator. So osascript can never parse the
   string (even a dash-leading one) as one of its own options.
2. **Terminal's `do script`.** This runs the string in the user's shell, and
   there is *no* argv boundary inside `do script` — the string is the command. So
   the only defense is to make the string structurally incapable of holding a
   shell metacharacter. That is enforced by validating every field with a strict
   allowlist BEFORE assembly:
   - `host` → `validateNetworkHost` (letters/digits/`.`/`-`/`:` only; no spaces,
     quotes, `@`, `;`, `$`, backticks, …).
   - `user` → `validateSSHUser`: POSIX-ish username shape
     `^[A-Za-z_][A-Za-z0-9._-]*$`. Crucially excludes `@` (so a value cannot
     append a second `user@otherhost`), spaces, and every shell metacharacter.
   - `key` → an existing regular file **inside `~/.ssh`** whose absolute path
     matches a shell-safe allowlist (`^[A-Za-z0-9._/-]+$`). Confinement is
     **symlink-safe**: the path is resolved with `filepath.EvalSymlinks` and the
     REAL target must still sit under `~/.ssh`, so a symlink planted in `~/.ssh`
     cannot redirect `ssh -i` at an arbitrary file (an in-tree symlink stays
     allowed). The safe-path check stops a path with a space/metacharacter
     reaching the shell string.
   - The **destination** token (`user@host`) is re-checked by a slightly wider
     final gate (`sshSafeTokenPattern`, which adds `:` and `@`) so an IPv6 host
     literal like `fe80::1` — already vetted by `validateNetworkHost` — is not
     rejected, without loosening what a key PATH may contain.
   - `port` → an integer in 1–65535.
   `buildSSHCommand` then re-checks every assembled token against the same
   safe-character set as a final gate, so a field added later without its own
   guard still cannot smuggle a metacharacter into the `do script` string.

Regression tests feed hostile `host`/`user`/`key` values (including the
`-e`-as-host case CLAUDE.md §4 calls for) and assert each is rejected before any
command is built.

## Key-selection precedence

At stage time `resolveSSHKey` chooses the identity deterministically:

1. an explicit `key` argument (validated as above) wins;
2. else the `IdentityFile` from a matching `~/.ssh/config` Host block (matched by
   alias or HostName);
3. else, if `~/.ssh` holds exactly one key pair, that sole key;
4. else, when several keys exist and nothing disambiguated, a **stage-time error**
   listing the candidates so the human can pass `key` to pick one.

Deviation from the plan's literal 4-step list, documented here: when `~/.ssh`
holds **no** keys at all, `ssh_connect` does not error — it omits `-i` and lets
`ssh` fall back to its own defaults (an agent identity or a password prompt in
the Terminal window). Erroring "pick a key" when there are no keys would be
confusing and would make the op unusable on a password-auth-only machine, so the
"ask the user to pick" error is reserved for the genuinely ambiguous
multiple-keys case. A key path containing a space (e.g. a home directory with a
space) is rejected with a clear message rather than silently mangled — an
accepted edge-case limitation.

## TCC / permissions

Opening Terminal via AppleScript needs **Automation** access to Terminal
(System Settings → Privacy & Security → Automation); the first connect prompts
for it. The README documents this and the tool description notes it, matching the
established actionable-error convention for the other AppleScript-backed domains.

## Manual on-device smoke test (not in CI)

`MCP_LIVE_SSH=1 go test ./internal/engine/ -run TestListSSHKeysLive` fingerprints
the developer's real `~/.ssh` and asserts no private-key marker leaks. The
`ssh_connect` live path is a manual case: stage a connect to a real host, execute
it, and confirm a Terminal window opens on the right command and the Automation
grant flows on first use.

## Dropped / out of scope

- The server never reads or transmits a password or passphrase — authentication
  is entirely the user's, in the Terminal window.
- No `-o` option passthrough (e.g. `StrictHostKeyChecking`): keeping the command
  to validated fields only is what makes the two-interpreter safety tractable.
