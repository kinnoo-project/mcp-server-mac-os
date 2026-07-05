// mutate_ssh.go implements the one mutating operation of the SSH capability set
// (V10): ssh_connect, which opens Terminal.app on a fully-constructed `ssh`
// command. The read-only inventory operations (list_ssh_keys, list_ssh_hosts)
// live in builtins_ssh.go.
//
// # Why this hands off to Terminal instead of running ssh itself
//
// An SSH session is interactive: it prompts for a host-key fingerprint on first
// connect, and for a password or key passphrase. This server's non-interactive
// stdio transport cannot answer those prompts, and it must NEVER see a password.
// So ssh_connect does not run ssh at all — it stages an AppleScript that opens a
// new Terminal window and types the ssh command there, where the human completes
// authentication under their own control. The capability's effect is therefore
// "a Terminal session was started"; there is no inverse (you close the window
// yourself), so it is classified irreversible and always staged for confirmation.
//
// # The "data, never code" guarantee across two interpreters
//
// The forward command is the shared, hardened osascript seam (osascriptCommand),
// which passes the ssh command string as a single DATA argument bound to the
// script's `on run argv` after a "--" terminator — so osascript can never parse
// it as one of its own options. But the string then reaches a SECOND interpreter:
// Terminal's `do script` runs it in the user's shell. There is no argv boundary
// inside `do script`, so the only defense is to make the command string itself
// incapable of holding a shell metacharacter. That is enforced by validating
// every field with a strict allowlist BEFORE assembly:
//
//   - host  → validateNetworkHost (letters/digits/'.'/'-'/':' only; no spaces,
//     quotes, '@', ';', '$', backticks, …)
//   - user  → validateSSHUser (POSIX-ish username charset, no '@'/space/meta)
//   - key   → an existing file under ~/.ssh whose absolute path contains only
//     safe path characters (no space/quote/backslash/meta)
//   - port  → an integer in 1–65535
//
// buildSSHCommand additionally re-checks the fully assembled tokens against the
// same safe-character set as a belt-and-suspenders final gate, so a future field
// added without its own guard still cannot smuggle a shell metacharacter into the
// `do script` string. The regression tests in mutate_ssh_test.go feed hostile
// host/user/key values and assert each is rejected before any command is built.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// sshTerminalScript opens Terminal.app and runs the single ssh command passed as
// item 1 of argv. The script body is a FIXED constant (never concatenated with
// model input); the command string arrives as data via osascriptCommand's "--"
// terminator. `activate` brings Terminal forward so the user sees the session and
// any host-key/password prompt.
const sshTerminalScript = `on run argv
	tell application "Terminal"
		activate
		do script (item 1 of argv)
	end tell
end run`

// sshUserPattern is the allowlist a login username must match in full: it must
// start with a letter or underscore and otherwise contain only letters, digits,
// '.', '_', or '-'. This is the conventional POSIX username shape and, crucially,
// it excludes '@' (so a value can never inject an extra "user@otherhost"), spaces,
// and every shell metacharacter — which is what keeps the composed `user@host`
// safe inside Terminal's `do script`.
var sshUserPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9._-]*$`)

// sshSafePathPattern constrains a key's absolute path to characters that cannot
// carry any meaning to a shell: letters, digits, and the path/name punctuation
// '/', '.', '_', '-'. A space or any shell metacharacter fails it. Because the
// key path is embedded (unquoted) in the `do script` command string, this is the
// guarantee that a legitimate key path cannot break out of the ssh command.
var sshSafePathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// sshSafeTokenPattern is the final-gate allowlist for a fully-assembled command
// token. It is sshSafePathPattern widened by ':' and '@' — both of which are
// inert in a shell word but appear legitimately in the destination token
// (`user@host`, and IPv6 host literals such as `fe80::1`, which validateNetworkHost
// already vets). Keeping the key-PATH pattern stricter (no ':'/'@') while allowing
// them HERE means an IPv6 host is accepted without loosening what a key path may
// contain.
var sshSafeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._:/@-]+$`)

// maxSSHUserLen bounds a username so a pathological value cannot bloat the staged
// command; 64 is comfortably above any real login name.
const maxSSHUserLen = 64

// validateSSHUser guards the model-controlled login username against the
// sshUserPattern allowlist. A leading dash is impossible under the pattern (the
// first character must be a letter or underscore), so a value can never be read
// as an ssh option, and '@'/space/metacharacters are excluded so the composed
// `user@host` is a single inert token.
func validateSSHUser(raw string) (string, error) {
	user := strings.TrimSpace(raw)
	if user == "" {
		return "", fmt.Errorf("ssh_connect: 'user' is required (the login name on the server, e.g. ubuntu)")
	}
	if len(user) > maxSSHUserLen {
		return "", fmt.Errorf("ssh_connect: user %q is too long (max %d characters)", user, maxSSHUserLen)
	}
	if !sshUserPattern.MatchString(user) {
		return "", fmt.Errorf("ssh_connect: user %q is not a valid login name; use letters, digits, and '._-' only, starting with a letter or underscore", user)
	}
	return user, nil
}

// validatePort bounds an optional TCP port to the legal 1–65535 range. It
// returns ok=false when no port was supplied (the caller then omits -p and lets
// ssh default to 22), and an error only for an out-of-range value.
func validatePort(in map[string]any) (port int, ok bool, err error) {
	n, present := getInt(in, "port")
	if !present {
		return 0, false, nil
	}
	if n < 1 || n > 65535 {
		return 0, false, fmt.Errorf("ssh_connect: port %d is out of range (must be 1–65535)", n)
	}
	return n, true, nil
}

// validateSSHKeyPath validates an EXPLICIT key argument: it must be dash-free,
// resolve to an existing regular file located inside ~/.ssh, and have a
// shell-safe absolute path. Confining the key to ~/.ssh means the model cannot
// aim `ssh -i` at an arbitrary file on disk (e.g. to probe for a file's
// existence), and the safe-path check keeps the path from carrying a shell
// metacharacter into the Terminal command. A leading '~' is expanded to the home
// directory first so the natural "~/.ssh/id_ed25519" form works.
func validateSSHKeyPath(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("ssh_connect: 'key' was given but is empty")
	}
	if strings.HasPrefix(key, "-") {
		return "", fmt.Errorf("ssh_connect: key %q begins with '-' and is not allowed", key)
	}
	dir, err := sshDir()
	if err != nil {
		return "", err
	}
	expanded := key
	if key == "~" || strings.HasPrefix(key, "~/") {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("ssh_connect: could not expand '~' in key %q: %w", key, herr)
		}
		expanded = filepath.Join(home, strings.TrimPrefix(key, "~"))
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("ssh_connect: resolving key %q: %w", key, err)
	}
	// Confine to ~/.ssh: the resolved path must sit inside that directory. The
	// trailing separator prevents a sibling like "~/.ssh-other" from passing.
	if abs != dir && !strings.HasPrefix(abs, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("ssh_connect: key %q must be a file inside ~/.ssh", key)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("ssh_connect: key %q does not exist; run list_ssh_keys to see available keys", abs)
		}
		return "", fmt.Errorf("ssh_connect: cannot inspect key %q: %w", abs, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("ssh_connect: key %q is a directory, not a key file", abs)
	}
	// Confinement must survive symlinks: a prefix check on the literal path lets a
	// symlink PLANTED inside ~/.ssh redirect `ssh -i` at an arbitrary file (e.g.
	// ~/.ssh/link → /etc/shadow), defeating the "confined to ~/.ssh" guarantee. So
	// resolve symlinks on both the key and the ~/.ssh directory and re-check the
	// REAL target is still inside. Both paths exist here, so EvalSymlinks succeeds;
	// the directory falls back to its literal form only if it is somehow
	// unresolvable.
	realAbs, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("ssh_connect: cannot resolve key %q: %w", abs, err)
	}
	realDir := dir
	if rd, derr := filepath.EvalSymlinks(dir); derr == nil {
		realDir = rd
	}
	if realAbs != realDir && !strings.HasPrefix(realAbs, realDir+string(os.PathSeparator)) {
		return "", fmt.Errorf("ssh_connect: key %q resolves to %q, which is outside ~/.ssh", key, realAbs)
	}
	// Guard the path we will actually emit (abs), not the resolved target.
	if !sshSafePathPattern.MatchString(abs) {
		return "", fmt.Errorf("ssh_connect: key path %q contains characters that cannot be used safely in a Terminal command; move the key to a path without spaces or punctuation", abs)
	}
	return abs, nil
}

// resolveSSHKey picks which identity file (if any) ssh_connect should pass with
// -i, applying the precedence: an explicit key argument wins; else an
// IdentityFile from a matching ~/.ssh/config Host block; else, if ~/.ssh holds
// exactly one key pair, that sole key; else, when several keys exist and nothing
// disambiguated, a stage-time error listing the candidates so the human can pick.
// When ~/.ssh holds NO keys at all, it returns "" (no error): the command then
// omits -i and lets ssh fall back to its own defaults (an agent identity or
// password auth). The returned string is either "" or a validated, shell-safe
// absolute path.
func resolveSSHKey(host string, in map[string]any) (keyPath string, source string, err error) {
	// 1. Explicit key argument wins.
	if raw, ok := getString(in, "key"); ok && strings.TrimSpace(raw) != "" {
		abs, verr := validateSSHKeyPath(raw)
		if verr != nil {
			return "", "", verr
		}
		return abs, "the key you specified", nil
	}

	dir, err := sshDir()
	if err != nil {
		return "", "", err
	}

	// 2. IdentityFile from a matching ~/.ssh/config Host block.
	if idFile := identityFileForHost(dir, host); idFile != "" {
		abs, verr := validateSSHKeyPath(idFile)
		if verr == nil {
			return abs, fmt.Sprintf("the IdentityFile for %q in ~/.ssh/config", host), nil
		}
		// A config IdentityFile that fails validation (missing, outside ~/.ssh, or
		// unsafe path) is not fatal — fall through to key discovery rather than
		// blocking the connect on a stale config entry.
	}

	// 3./4. Discover key pairs in ~/.ssh (public keys with a private counterpart).
	names, paths := discoverPrivateKeys(dir)
	switch len(paths) {
	case 0:
		// No keys at all: connect without -i and let ssh use its defaults.
		return "", "", nil
	case 1:
		if !sshSafePathPattern.MatchString(paths[0]) {
			return "", "", fmt.Errorf("ssh_connect: your only SSH key %q has a path that cannot be used safely in a Terminal command", paths[0])
		}
		return paths[0], "the only key in ~/.ssh", nil
	default:
		return "", "", fmt.Errorf(
			"ssh_connect: ~/.ssh has several keys (%s) and none is selected by ~/.ssh/config for %q; pass the 'key' parameter to choose one (run list_ssh_keys for details)",
			strings.Join(names, ", "), host)
	}
}

// identityFileForHost returns the IdentityFile declared for a host in
// ~/.ssh/config, matching either a Host alias exactly or a block whose HostName
// equals the given host. It returns "" when the config is absent/unparseable or
// no block matches. Any parse error is treated as "no match" — a broken config
// must not block a connect the user could otherwise make.
func identityFileForHost(sshDirPath, host string) string {
	hosts, err := parseSSHConfigHosts(filepath.Join(sshDirPath, "config"))
	if err != nil {
		return ""
	}
	for _, h := range hosts {
		if h.identityFile == "" {
			continue
		}
		if h.alias == host || h.hostName == host {
			return h.identityFile
		}
	}
	return ""
}

// discoverPrivateKeys returns the base names and absolute paths of the key pairs
// in ~/.ssh: every public (.pub) file whose private counterpart also exists. It
// reads no private key bytes — presence is tested by os.Stat only — and returns
// empty slices when ~/.ssh is absent or holds no complete pairs.
func discoverPrivateKeys(sshDirPath string) (names, paths []string) {
	entries, err := os.ReadDir(sshDirPath)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".pub")
		privPath := filepath.Join(sshDirPath, base)
		// Require a REGULAR file: a directory (or other non-file entry) named like a
		// key must not be offered as `ssh -i <dir>`, which would fail confusingly in
		// Terminal. regularFileExists follows a symlink but rejects a directory.
		if regularFileExists(privPath) {
			names = append(names, base)
			paths = append(paths, privPath)
		}
	}
	return names, paths
}

// buildSSHCommand assembles the ssh command string that Terminal will run:
// `ssh [-i <key>] [-p <port>] <user>@<host>`. Every input has already been
// validated by its own guard; this function re-checks each assembled token
// against the shell-safe allowlist as a final gate, so even a field added later
// without its own guard cannot introduce a space or metacharacter into the
// string that reaches Terminal's `do script`.
func buildSSHCommand(user, host, keyPath string, port int, hasPort bool) (string, error) {
	tokens := []string{"ssh"}
	if keyPath != "" {
		tokens = append(tokens, "-i", keyPath)
	}
	if hasPort {
		tokens = append(tokens, "-p", fmt.Sprintf("%d", port))
	}
	tokens = append(tokens, user+"@"+host)

	// Final belt-and-suspenders: no assembled token may contain whitespace or a
	// character outside the shell-safe set. sshSafeTokenPattern permits ':' and '@'
	// so the destination token (user@host, incl. an IPv6 host) passes, while a
	// space or shell metacharacter still fails. A failure indicates a guard gap
	// upstream.
	for _, tok := range tokens {
		if tok != "" && !sshSafeTokenPattern.MatchString(tok) {
			return "", fmt.Errorf("ssh_connect: refusing to build a command with unsafe token %q", tok)
		}
	}
	return strings.Join(tokens, " "), nil
}

// stageSSHConnect validates every field, resolves the identity key, assembles the
// ssh command string, and stages an AppleScript that opens Terminal on it. There
// is NO inverse — a started Terminal session cannot be un-started from here — so
// the plan is irreversible and the preview says how to end it (close the window).
func stageSSHConnect(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	rawHost, _ := getString(in, "host")
	host := strings.TrimSpace(rawHost)
	if err := validateNetworkHost(host); err != nil {
		return nil, fmt.Errorf("ssh_connect: %w", err)
	}

	rawUser, _ := getString(in, "user")
	user, err := validateSSHUser(rawUser)
	if err != nil {
		return nil, err
	}

	port, hasPort, err := validatePort(in)
	if err != nil {
		return nil, err
	}

	keyPath, keySource, err := resolveSSHKey(host, in)
	if err != nil {
		return nil, err
	}

	command, err := buildSSHCommand(user, host, keyPath, port, hasPort)
	if err != nil {
		return nil, err
	}

	// Build the human-facing preview: state exactly what will run and which key
	// (if any) was chosen and why, so the person approving sees the real command.
	var preview strings.Builder
	fmt.Fprintf(&preview, "Open Terminal and run: %s", command)
	if keyPath != "" {
		fmt.Fprintf(&preview, "\nAuthentication key: %s (%s).", keyPath, keySource)
	} else {
		preview.WriteString("\nNo key selected — ssh will use its defaults (an agent identity or a password prompt).")
	}
	preview.WriteString("\nThe interactive session (host-key confirmation, password/passphrase) happens in that Terminal window, under your control; this server never sees your password. To end the session, close the Terminal window.")

	return &StagedPlan{
		Preview: preview.String(),
		// The command string is a single DATA argument; osascriptCommand inserts
		// the "--" terminator so osascript cannot parse it as an option.
		Forward: osascriptCommand(sshTerminalScript, command),
		Inverse: nil, // irreversible: a started Terminal session is closed by the user
	}, nil
}
