// builtins_ssh.go implements the two read-only SSH-inventory builtins of the
// `network` domain (V10): list_ssh_keys and list_ssh_hosts. Together they let the
// agent answer "what SSH keys do I have" and "what servers can I ssh to" before
// composing an ssh_connect (which lives on the mutation seam in mutate_ssh.go).
//
// # Private-key safety is the defining constraint
//
// Neither builtin ever reads a private key's bytes. list_ssh_keys fingerprints
// ONLY the public (.pub) half of each pair via `ssh-keygen -l -f <pub>`, and
// list_ssh_hosts is a pure in-process parse of ~/.ssh/config that reads no key
// material at all. This mirrors the keychain domain's "metadata only, never the
// secret" posture (find_credential): the inventory is useful without ever
// exposing anything that could authenticate as the user.
//
// # No injection surface
//
// Both builtins take NO parameters — every path they touch is discovered by
// enumerating ~/.ssh, never supplied by the model — so there is nothing to guard
// against option/shell injection here. The one binary invoked (ssh-keygen) is
// only ever handed an absolute .pub path this code computed itself. The
// model-controlled inputs (host/user/key) all belong to ssh_connect, which
// carries its own guards in mutate_ssh.go.
package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// sshDir returns the absolute path to the current user's ~/.ssh directory. It is
// the single source of that path for both builtins and the ssh_connect mutator,
// so the "keys and config live under ~/.ssh" assumption is stated in exactly one
// place.
func sshDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine your home directory: %w", err)
	}
	return filepath.Join(home, ".ssh"), nil
}

// sshKey is one enumerated key pair: the base name (without the .pub suffix), the
// key type and comment parsed from its fingerprint line, the fingerprint itself,
// and whether the matching PRIVATE key file is present on disk. hasPrivate is
// derived purely from the file's existence — the private bytes are never opened.
type sshKey struct {
	name        string
	keyType     string
	fingerprint string
	comment     string
	hasPrivate  bool
}

// runListSSHKeys enumerates the key pairs in ~/.ssh by scanning for public
// (.pub) files and fingerprinting each with `ssh-keygen -l -f <pub>`. It reports
// the type, fingerprint, comment, and whether the private counterpart exists,
// WITHOUT ever reading a private key. A missing ~/.ssh directory is reported as
// "no keys" rather than an error, so the common no-keys case reads cleanly.
func runListSSHKeys(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	dir, err := sshDir()
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "No ~/.ssh directory exists on this Mac, so there are no SSH keys to list.", nil
		}
		return "", fmt.Errorf("could not read %s: %w", dir, err)
	}

	bin, err := policy.ResolveBinary("ssh-keygen")
	if err != nil {
		return "", err
	}

	var keys []sshKey
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		pubPath := filepath.Join(dir, e.Name())
		// The .pub path is one we computed from a directory listing (absolute,
		// leading '/'), never model input, so it can never be read as a flag and
		// needs no dash-guard. -l prints the fingerprint of the given key file; we
		// point it only at the PUBLIC half, so no private material is read.
		res, err := runCommand(ctx, bin, "-l", "-f", pubPath)
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			// ssh-keygen refused this file (not a valid public key, e.g. a stray
			// .pub). Skip it rather than fail the whole inventory.
			continue
		}
		keyType, fingerprint, comment := parseFingerprintLine(res.Stdout)
		base := strings.TrimSuffix(e.Name(), ".pub")
		key := sshKey{
			name:        base,
			keyType:     keyType,
			fingerprint: fingerprint,
			comment:     comment,
			// The private key is the same file without ".pub"; test only for its
			// EXISTENCE as a regular file — never open it.
			hasPrivate: regularFileExists(filepath.Join(dir, base)),
		}
		keys = append(keys, key)
	}

	if len(keys) == 0 {
		return "No SSH public keys (*.pub) were found in ~/.ssh.", nil
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].name < keys[j].name })

	var b strings.Builder
	fmt.Fprintf(&b, "%d SSH key pair(s) in ~/.ssh:\n", len(keys))
	for _, k := range keys {
		priv := "private key present"
		if !k.hasPrivate {
			priv = "PUBLIC ONLY (no private key on this Mac)"
		}
		fmt.Fprintf(&b, "\n  %s\n    type:        %s\n    fingerprint: %s\n", k.name, k.keyType, k.fingerprint)
		if k.comment != "" {
			fmt.Fprintf(&b, "    comment:     %s\n", k.comment)
		}
		fmt.Fprintf(&b, "    %s\n", priv)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// parseFingerprintLine pulls the key type, fingerprint, and comment out of a
// single `ssh-keygen -l` line, whose shape is:
//
//	256 SHA256:abc123… user@host (ED25519)
//
// i.e. "<bits> <fingerprint> <comment...> (<TYPE>)". The comment can contain
// spaces (or be absent), and the parenthesised type is always the last token, so
// the type is taken from the trailing "(...)" and the comment is whatever sits
// between the fingerprint and that type. Any field that cannot be found comes
// back empty rather than erroring, so an unusual line degrades gracefully.
func parseFingerprintLine(stdout string) (keyType, fingerprint, comment string) {
	line := strings.TrimSpace(stdout)
	if line == "" {
		return "", "", ""
	}
	// The type is the final parenthesised token; strip it off first so it does not
	// get folded into the comment.
	if open := strings.LastIndex(line, "("); open >= 0 && strings.HasSuffix(line, ")") {
		keyType = strings.TrimSpace(line[open+1 : len(line)-1])
		line = strings.TrimSpace(line[:open])
	}
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		fingerprint = fields[1]
		if len(fields) > 2 {
			comment = strings.Join(fields[2:], " ")
		}
	}
	return keyType, fingerprint, comment
}

// sshConfigHost is one `Host` block distilled from ~/.ssh/config: the alias(es)
// the block matches plus the effective HostName, User, IdentityFile, and Port it
// declares. Fields the block does not set are left empty.
type sshConfigHost struct {
	alias        string
	hostName     string
	user         string
	identityFile string
	port         string
}

// runListSSHHosts parses ~/.ssh/config in-process and reports its Host blocks. A
// missing config is reported as "none configured" rather than an error. Wildcard
// blocks (e.g. `Host *`) are skipped in the listing because they are defaults
// applied across hosts, not a concrete server one would "ssh to".
func runListSSHHosts(_ context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	dir, err := sshDir()
	if err != nil {
		return "", err
	}
	hosts, err := parseSSHConfigHosts(filepath.Join(dir, "config"))
	if err != nil {
		if os.IsNotExist(err) {
			return "No ~/.ssh/config file exists on this Mac, so there are no configured SSH hosts.", nil
		}
		return "", err
	}
	if len(hosts) == 0 {
		return "No concrete Host entries are defined in ~/.ssh/config.", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d SSH host(s) configured in ~/.ssh/config:\n", len(hosts))
	for _, h := range hosts {
		fmt.Fprintf(&b, "\n  %s\n", h.alias)
		if h.hostName != "" {
			fmt.Fprintf(&b, "    hostname: %s\n", h.hostName)
		}
		if h.user != "" {
			fmt.Fprintf(&b, "    user:     %s\n", h.user)
		}
		if h.port != "" {
			fmt.Fprintf(&b, "    port:     %s\n", h.port)
		}
		if h.identityFile != "" {
			fmt.Fprintf(&b, "    identity: %s\n", h.identityFile)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// parseSSHConfigHosts reads an OpenSSH client config file and returns one
// sshConfigHost per concrete (non-wildcard) alias. The parse is deliberately
// small and forgiving: OpenSSH treats keywords case-insensitively and separated
// by whitespace or '=', so each line is lowercased for its keyword and split on
// the first run of separators. A `Host` line opens a new block (and a block with
// several aliases yields one entry per alias, all sharing the block's settings);
// subsequent keywords fill the current block(s). Blocks whose alias contains a
// glob character ('*' or '?') are dropped from the result — those are default
// stanzas, not addressable servers. Comments (`#`) and blank lines are ignored.
func parseSSHConfigHosts(path string) ([]sshConfigHost, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// order preserves first-seen alias order for a stable listing; byAlias lets a
	// later keyword line update every block opened by the current Host line.
	var order []string
	byAlias := make(map[string]*sshConfigHost)
	var current []*sshConfigHost // blocks opened by the most recent Host line

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keyword, value := splitConfigLine(line)
		switch strings.ToLower(keyword) {
		case "host":
			current = nil
			for _, alias := range strings.Fields(value) {
				if strings.ContainsAny(alias, "*?") {
					continue // a default/wildcard stanza, not a concrete server
				}
				h, ok := byAlias[alias]
				if !ok {
					h = &sshConfigHost{alias: alias}
					byAlias[alias] = h
					order = append(order, alias)
				}
				current = append(current, h)
			}
		case "hostname":
			for _, h := range current {
				h.hostName = value
			}
		case "user":
			for _, h := range current {
				h.user = value
			}
		case "identityfile":
			for _, h := range current {
				h.identityFile = value
			}
		case "port":
			for _, h := range current {
				h.port = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	out := make([]sshConfigHost, 0, len(order))
	for _, alias := range order {
		out = append(out, *byAlias[alias])
	}
	return out, nil
}

// splitConfigLine splits an ssh_config line into its keyword and value. OpenSSH
// accepts either whitespace or '=' (optionally surrounded by whitespace) between
// the two, so the split happens at the first '=' or run of spaces/tabs, whichever
// comes first, and both halves are trimmed.
func splitConfigLine(line string) (keyword, value string) {
	idx := strings.IndexAny(line, " \t=")
	if idx < 0 {
		return line, ""
	}
	keyword = line[:idx]
	value = strings.TrimLeft(line[idx:], " \t=")
	// Strip an inline comment. A stray "# prod" after a value (a common ssh_config
	// style) would otherwise leak into HostName/User/IdentityFile and break
	// matching and the listing. None of the values we parse — hostnames, POSIX
	// usernames, key paths, ports — legitimately contains '#', so cutting at the
	// first '#' is safe.
	if i := strings.IndexByte(value, '#'); i >= 0 {
		value = value[:i]
	}
	return keyword, strings.TrimSpace(value)
}

// regularFileExists reports whether path names an existing REGULAR file
// (following symlinks), never a directory or other special entry. It is used to
// test for a private key's presence purely from the filesystem — the file's
// contents are never read — while excluding a directory that happens to be named
// like a key (which must not be reported as, or offered as, a usable key).
func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
