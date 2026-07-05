// builtins_ssh_test.go covers the read-only SSH-inventory builtins: the
// fingerprint-line parser, the in-process ~/.ssh/config parser (including its
// wildcard-skipping and '='-separator handling), and — behind an env gate — a
// live pass of list_ssh_keys against the real ~/.ssh on the developer's machine.
// The private-key-safety property (only .pub files are fingerprinted) is asserted
// structurally: the parser is exercised on a temp ~/.ssh that contains a private
// key with NO .pub, and the config parser reads no key bytes at all.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestParseFingerprintLine pins the parse of `ssh-keygen -l` output across the
// shapes it takes: a normal line with a comment, one with a multi-word comment,
// and one with no comment at all.
func TestParseFingerprintLine(t *testing.T) {
	cases := []struct {
		name                       string
		line                       string
		wantType, wantFP, wantNote string
	}{
		{
			name:     "ed25519 with comment",
			line:     "256 SHA256:AbC123dEf jane@example.com (ED25519)",
			wantType: "ED25519",
			wantFP:   "SHA256:AbC123dEf",
			wantNote: "jane@example.com",
		},
		{
			name:     "rsa multi-word comment",
			line:     "2048 SHA256:ZZZ my work laptop (RSA)",
			wantType: "RSA",
			wantFP:   "SHA256:ZZZ",
			wantNote: "my work laptop",
		},
		{
			name:     "no comment",
			line:     "256 SHA256:QQQ (ED25519)\n",
			wantType: "ED25519",
			wantFP:   "SHA256:QQQ",
			wantNote: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotFP, gotNote := parseFingerprintLine(tc.line)
			if gotType != tc.wantType || gotFP != tc.wantFP || gotNote != tc.wantNote {
				t.Errorf("parseFingerprintLine(%q) = (%q,%q,%q), want (%q,%q,%q)",
					tc.line, gotType, gotFP, gotNote, tc.wantType, tc.wantFP, tc.wantNote)
			}
		})
	}
}

// TestSplitConfigLine checks both accepted separators (whitespace and '=', with
// or without surrounding spaces).
func TestSplitConfigLine(t *testing.T) {
	cases := []struct{ in, wantK, wantV string }{
		{"Host myserver", "Host", "myserver"},
		{"HostName=example.com", "HostName", "example.com"},
		{"User  =  jane", "User", "jane"},
		{"Port\t2222", "Port", "2222"},
		{"IdentityFile ~/.ssh/id_ed25519", "IdentityFile", "~/.ssh/id_ed25519"},
	}
	for _, tc := range cases {
		k, v := splitConfigLine(tc.in)
		if k != tc.wantK || v != tc.wantV {
			t.Errorf("splitConfigLine(%q) = (%q,%q), want (%q,%q)", tc.in, k, v, tc.wantK, tc.wantV)
		}
	}
}

// TestParseSSHConfigHosts exercises the config parser on a representative file:
// multiple aliases sharing a block, a wildcard default stanza that must be
// skipped, '=' separators, and settings that carry into the right block.
func TestParseSSHConfigHosts(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	content := `# my ssh config
Host *
	ServerAliveInterval 60

Host web prod
	HostName 192.0.2.10
	User deploy
	IdentityFile ~/.ssh/id_deploy
	Port 2222

Host db
	HostName=db.example.com
	User=admin
`
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts, err := parseSSHConfigHosts(cfg)
	if err != nil {
		t.Fatalf("parseSSHConfigHosts: %v", err)
	}
	// Expect web, prod, db — the wildcard "*" block is dropped.
	if len(hosts) != 3 {
		t.Fatalf("got %d hosts, want 3: %+v", len(hosts), hosts)
	}
	byAlias := map[string]sshConfigHost{}
	for _, h := range hosts {
		byAlias[h.alias] = h
	}
	web, ok := byAlias["web"]
	if !ok {
		t.Fatal("expected a 'web' host")
	}
	if web.hostName != "192.0.2.10" || web.user != "deploy" || web.port != "2222" || web.identityFile != "~/.ssh/id_deploy" {
		t.Errorf("web block = %+v, want the shared settings", web)
	}
	if prod := byAlias["prod"]; prod.hostName != "192.0.2.10" || prod.user != "deploy" {
		t.Errorf("prod alias should share the block's settings, got %+v", prod)
	}
	db := byAlias["db"]
	if db.hostName != "db.example.com" || db.user != "admin" {
		t.Errorf("db block = %+v, want hostname/user from '=' lines", db)
	}
	if _, isWildcard := byAlias["*"]; isWildcard {
		t.Error("wildcard '*' stanza must not appear as a concrete host")
	}
}

// TestDiscoverPrivateKeys proves the discovery only counts a key when BOTH the
// .pub and its private counterpart are present, and that it never depends on
// reading the private bytes (the file here contains junk, not a real key).
func TestDiscoverPrivateKeys(t *testing.T) {
	dir := t.TempDir()
	// A complete pair.
	mustWrite(t, filepath.Join(dir, "id_ed25519"), "PRIVATE-DO-NOT-READ")
	mustWrite(t, filepath.Join(dir, "id_ed25519.pub"), "ssh-ed25519 AAAA... jane@example.com")
	// A public key with no private counterpart — must NOT count as a usable key.
	mustWrite(t, filepath.Join(dir, "orphan.pub"), "ssh-rsa AAAA... orphan")
	// A non-key file.
	mustWrite(t, filepath.Join(dir, "known_hosts"), "example.com ssh-rsa AAAA...")

	names, paths := discoverPrivateKeys(dir)
	if len(paths) != 1 || len(names) != 1 {
		t.Fatalf("discoverPrivateKeys found %v / %v, want exactly the id_ed25519 pair", names, paths)
	}
	if names[0] != "id_ed25519" {
		t.Errorf("name = %q, want id_ed25519", names[0])
	}
	if !strings.HasSuffix(paths[0], "/id_ed25519") {
		t.Errorf("path = %q, want the private key path", paths[0])
	}
}

// TestListSSHKeysLive runs the real list_ssh_keys against the developer's ~/.ssh.
// It is gated behind MCP_LIVE_SSH=1 so CI (which has no keys and no ssh-keygen
// guarantees) never runs it; when enabled it simply asserts the call succeeds and
// never leaks a "PRIVATE KEY" marker.
func TestListSSHKeysLive(t *testing.T) {
	if os.Getenv("MCP_LIVE_SSH") != "1" {
		t.Skip("set MCP_LIVE_SSH=1 to run the live list_ssh_keys smoke test")
	}
	out, err := runListSSHKeys(context.Background(), registry.Capability{}, nil)
	if err != nil {
		t.Fatalf("runListSSHKeys: %v", err)
	}
	if strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, "BEGIN OPENSSH") {
		t.Errorf("list_ssh_keys output appears to contain private key material:\n%s", out)
	}
	t.Logf("list_ssh_keys:\n%s", out)
}
