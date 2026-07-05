// mutate_ssh_test.go is the security gate for ssh_connect. Because the staged
// command string crosses two interpreters (osascript, then Terminal's `do
// script` shell), the tests here concentrate on proving that no hostile
// host/user/key/port value can escape the field allowlists or reach the command
// string as anything but an inert token: the accept/reject tables for each
// validator, the key-selection precedence, the "-e as host lands as data"
// regression, and the pinned forward argv shape (script + '--' + command).
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// withTempHome points os.UserHomeDir at a fresh temp directory containing a
// ~/.ssh, so key-resolution tests are hermetic and never touch the developer's
// real keys. It returns the ~/.ssh path and restores HOME on cleanup.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	return ssh
}

// TestValidateSSHUser is the accept/reject table for login usernames. The reject
// rows are the security-relevant ones: a leading dash (option injection), an '@'
// (which would append a second host), and shell metacharacters/spaces (which
// would break out of the `do script` command).
func TestValidateSSHUser(t *testing.T) {
	good := []string{"ubuntu", "jane", "_svc", "a.b-c", "root", "deploy2"}
	for _, u := range good {
		if _, err := validateSSHUser(u); err != nil {
			t.Errorf("validateSSHUser(%q) rejected a valid name: %v", u, err)
		}
	}
	bad := []string{
		"", "-rf", "ja ne", "jane@evil", "a;b", "a$b", "`b`", "a&&b",
		"a\nb", "a\x00b", "1user", ".user", "-user",
	}
	for _, u := range bad {
		if _, err := validateSSHUser(u); err == nil {
			t.Errorf("validateSSHUser(%q) accepted a hostile/invalid name", u)
		}
	}
}

// TestValidatePort covers the range guard and the "absent = default" behaviour.
func TestValidatePort(t *testing.T) {
	if _, ok, err := validatePort(map[string]any{}); ok || err != nil {
		t.Errorf("absent port: got ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if p, ok, err := validatePort(map[string]any{"port": 2222}); !ok || err != nil || p != 2222 {
		t.Errorf("port 2222: got p=%d ok=%v err=%v", p, ok, err)
	}
	for _, bad := range []int{0, -1, 65536, 999999} {
		if _, _, err := validatePort(map[string]any{"port": bad}); err == nil {
			t.Errorf("validatePort(%d) accepted an out-of-range port", bad)
		}
	}
}

// TestValidateSSHKeyPath confines an explicit key to an existing file inside
// ~/.ssh and rejects dash-leading, missing, out-of-tree, and directory targets.
func TestValidateSSHKeyPath(t *testing.T) {
	ssh := withTempHome(t)
	keyPath := filepath.Join(ssh, "id_ed25519")
	mustWrite(t, keyPath, "PRIVATE")
	mustWrite(t, filepath.Join(ssh, "id_ed25519.pub"), "ssh-ed25519 AAAA")

	// Accept: an existing key given by absolute path and by "~/.ssh/..." form.
	if got, err := validateSSHKeyPath(keyPath); err != nil || got != keyPath {
		t.Errorf("validateSSHKeyPath(abs) = (%q,%v), want the abs path", got, err)
	}
	if got, err := validateSSHKeyPath("~/.ssh/id_ed25519"); err != nil || got != keyPath {
		t.Errorf("validateSSHKeyPath(~) = (%q,%v), want the abs path", got, err)
	}

	// Reject rows.
	home := filepath.Dir(ssh)
	outside := filepath.Join(home, "secret.txt")
	mustWrite(t, outside, "not a key here")
	bad := []struct{ name, in string }{
		{"empty", ""},
		{"dash-leading", "-rf"},
		{"missing", "~/.ssh/nope"},
		{"outside ~/.ssh", outside},
		{"sibling dir trick", home + "/.ssh-other/key"},
		{"directory", ssh},
	}
	for _, tc := range bad {
		if _, err := validateSSHKeyPath(tc.in); err == nil {
			t.Errorf("validateSSHKeyPath(%s=%q) accepted an invalid key", tc.name, tc.in)
		}
	}
}

// TestBuildSSHCommand pins the assembled command shapes and proves the final
// safe-token gate rejects a smuggled metacharacter even if a caller bypassed the
// field validators.
func TestBuildSSHCommand(t *testing.T) {
	cases := []struct {
		name            string
		user, host, key string
		port            int
		hasPort         bool
		want            string
	}{
		{"minimal", "jane", "192.0.2.10", "", 0, false, "ssh jane@192.0.2.10"},
		{"with key", "ubuntu", "host.example.com", "/Users/x/.ssh/id_ed25519", 0, false, "ssh -i /Users/x/.ssh/id_ed25519 ubuntu@host.example.com"},
		{"with port", "jane", "192.0.2.10", "", 2222, true, "ssh -p 2222 jane@192.0.2.10"},
		{"key and port", "root", "myserver", "/Users/x/.ssh/k", 22, true, "ssh -i /Users/x/.ssh/k -p 22 root@myserver"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildSSHCommand(tc.user, tc.host, tc.key, tc.port, tc.hasPort)
			if err != nil {
				t.Fatalf("buildSSHCommand: %v", err)
			}
			if got != tc.want {
				t.Errorf("buildSSHCommand = %q, want %q", got, tc.want)
			}
		})
	}
	// Final gate: a key path with a space (which the validators would have caught)
	// must still be refused by buildSSHCommand rather than emitted into the string.
	if _, err := buildSSHCommand("jane", "host", "/Users/John Doe/.ssh/k", 0, false); err == nil {
		t.Error("buildSSHCommand accepted a key path containing a space")
	}
}

// TestStageSSHConnect_RejectsHostileFields is the umbrella regression: each
// hostile field value must be refused at stage time, never reaching a built
// command. It specifically pins the "-e as host" case CLAUDE.md §4 calls for.
func TestStageSSHConnect_RejectsHostileFields(t *testing.T) {
	withTempHome(t)
	cases := []struct {
		name string
		in   map[string]any
	}{
		{"host -e", map[string]any{"host": "-e", "user": "jane"}},
		{"host with metachar", map[string]any{"host": "a;reboot", "user": "jane"}},
		{"host with space", map[string]any{"host": "a b", "user": "jane"}},
		{"user -rf", map[string]any{"host": "192.0.2.10", "user": "-rf"}},
		{"user @injection", map[string]any{"host": "192.0.2.10", "user": "jane@evil"}},
		{"empty host", map[string]any{"host": "", "user": "jane"}},
		{"empty user", map[string]any{"host": "192.0.2.10", "user": ""}},
		{"bad port", map[string]any{"host": "192.0.2.10", "user": "jane", "port": 70000}},
		{"key outside ssh", map[string]any{"host": "192.0.2.10", "user": "jane", "key": "/etc/passwd"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := stageSSHConnect(context.Background(), registry.Capability{}, tc.in); err == nil {
				t.Errorf("stageSSHConnect(%v) staged a hostile input instead of rejecting it", tc.in)
			}
		})
	}
}

// TestStageSSHConnect_KeySelectionPrecedence walks the four precedence branches:
// explicit key, config IdentityFile, sole key, and the ambiguous-multiple error;
// plus the no-keys fallback (connect without -i).
func TestStageSSHConnect_KeySelectionPrecedence(t *testing.T) {
	t.Run("no keys → no -i", func(t *testing.T) {
		withTempHome(t)
		plan := mustStageSSH(t, map[string]any{"host": "192.0.2.10", "user": "jane"})
		if cmd := stagedSSHCommand(t, plan); strings.Contains(cmd, "-i") {
			t.Errorf("expected no -i with an empty ~/.ssh, got %q", cmd)
		}
	})

	t.Run("sole key chosen", func(t *testing.T) {
		ssh := withTempHome(t)
		mustWrite(t, filepath.Join(ssh, "id_ed25519"), "PRIV")
		mustWrite(t, filepath.Join(ssh, "id_ed25519.pub"), "ssh-ed25519 AAAA")
		plan := mustStageSSH(t, map[string]any{"host": "192.0.2.10", "user": "jane"})
		cmd := stagedSSHCommand(t, plan)
		if !strings.Contains(cmd, "-i") || !strings.HasSuffix(cmd, "jane@192.0.2.10") {
			t.Errorf("expected the sole key via -i, got %q", cmd)
		}
	})

	t.Run("multiple keys → error", func(t *testing.T) {
		ssh := withTempHome(t)
		for _, n := range []string{"id_ed25519", "id_rsa"} {
			mustWrite(t, filepath.Join(ssh, n), "PRIV")
			mustWrite(t, filepath.Join(ssh, n+".pub"), "ssh-x AAAA")
		}
		if _, err := stageSSHConnect(context.Background(), registry.Capability{},
			map[string]any{"host": "192.0.2.10", "user": "jane"}); err == nil {
			t.Error("expected an ambiguity error when several keys exist and none is selected")
		}
	})

	t.Run("explicit key wins over sole", func(t *testing.T) {
		ssh := withTempHome(t)
		mustWrite(t, filepath.Join(ssh, "id_ed25519"), "PRIV")
		mustWrite(t, filepath.Join(ssh, "id_ed25519.pub"), "pub")
		mustWrite(t, filepath.Join(ssh, "id_special"), "PRIV")
		mustWrite(t, filepath.Join(ssh, "id_special.pub"), "pub")
		plan := mustStageSSH(t, map[string]any{"host": "192.0.2.10", "user": "jane", "key": "~/.ssh/id_special"})
		if cmd := stagedSSHCommand(t, plan); !strings.Contains(cmd, "id_special") {
			t.Errorf("explicit key should win, got %q", cmd)
		}
	})

	t.Run("config IdentityFile chosen", func(t *testing.T) {
		ssh := withTempHome(t)
		mustWrite(t, filepath.Join(ssh, "id_deploy"), "PRIV")
		mustWrite(t, filepath.Join(ssh, "id_deploy.pub"), "pub")
		mustWrite(t, filepath.Join(ssh, "id_other"), "PRIV")
		mustWrite(t, filepath.Join(ssh, "id_other.pub"), "pub")
		cfg := "Host prod\n\tHostName 192.0.2.10\n\tUser deploy\n\tIdentityFile ~/.ssh/id_deploy\n"
		mustWrite(t, filepath.Join(ssh, "config"), cfg)
		// Reference the host by its config alias — the IdentityFile should be picked
		// even though two keys exist (which would otherwise be ambiguous).
		plan := mustStageSSH(t, map[string]any{"host": "prod", "user": "deploy"})
		if cmd := stagedSSHCommand(t, plan); !strings.Contains(cmd, "id_deploy") {
			t.Errorf("config IdentityFile should be chosen, got %q", cmd)
		}
	})
}

// TestStageSSHConnect_ForwardArgvShape pins that the forward command is the
// osascript seam with the command string as data after '--', so the two-
// interpreter hardening is structurally in place.
func TestStageSSHConnect_ForwardArgvShape(t *testing.T) {
	withTempHome(t)
	plan := mustStageSSH(t, map[string]any{"host": "192.0.2.10", "user": "jane"})
	if plan.Forward.Binary != "osascript" {
		t.Fatalf("forward binary = %q, want osascript", plan.Forward.Binary)
	}
	if plan.Inverse != nil {
		t.Error("ssh_connect must have no inverse (a started session cannot be un-started)")
	}
	args := plan.Forward.Args
	// Expect: -e <script> -- <command>
	if len(args) != 4 || args[0] != "-e" || args[2] != "--" {
		t.Fatalf("forward argv = %q, want [-e <script> -- <command>]", args)
	}
	if args[1] != sshTerminalScript {
		t.Error("forward script is not the fixed sshTerminalScript constant")
	}
	if !strings.HasPrefix(args[3], "ssh ") {
		t.Errorf("command data = %q, want it to start with 'ssh '", args[3])
	}
}

// mustStageSSH stages ssh_connect and fails the test on error.
func mustStageSSH(t *testing.T, in map[string]any) *StagedPlan {
	t.Helper()
	plan, err := stageSSHConnect(context.Background(), registry.Capability{}, in)
	if err != nil {
		t.Fatalf("stageSSHConnect(%v): %v", in, err)
	}
	return plan
}

// stagedSSHCommand extracts the ssh command string (the argv element after '--')
// from a staged plan's forward osascript command.
func stagedSSHCommand(t *testing.T, plan *StagedPlan) string {
	t.Helper()
	args := plan.Forward.Args
	if len(args) != 4 || args[2] != "--" {
		t.Fatalf("unexpected forward argv shape: %q", args)
	}
	return args[3]
}
