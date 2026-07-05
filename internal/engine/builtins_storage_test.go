// builtins_storage_test.go covers the storage domain's read-only builtins: the
// argv-verb pins (also exercised by security_verbs_test.go), the shared
// volume-identifier guardrail and its per-operation hostile-input regressions
// required by CLAUDE.md §4, the tmutil list rendering helper, and — behind an
// env gate — a live pass against the real tmutil/diskutil.
package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestStorageArgvBuilders pins the exact read argument vectors so a future edit
// that slipped in a state-changing verb (tmutil delete, diskutil erase) fails
// here in addition to TestSecurity_ConstrainedBinaryVerbs.
func TestStorageArgvBuilders(t *testing.T) {
	cases := []struct {
		name string
		got  []string
		want string
	}{
		{"tmutilStatusArgs", tmutilStatusArgs(), "status"},
		{"tmutilLatestBackupArgs", tmutilLatestBackupArgs(), "latestbackup"},
		{"tmutilListBackupsArgs", tmutilListBackupsArgs(), "listbackups"},
		{"tmutilListSnapshotsArgs", tmutilListSnapshotsArgs(), "listlocalsnapshots /"},
		{"diskutilListArgs", diskutilListArgs(), "list"},
		{"diskutilInfoArgs", diskutilInfoArgs("disk2s1"), "info disk2s1"},
	}
	for _, tc := range cases {
		if got := strings.Join(tc.got, " "); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestValidateVolumeIdentifier is the accept/reject table for the shared storage
// input guardrail. The accepted forms are disk identifiers and single-component
// /Volumes mount paths; everything else — dash-leading flags, path traversal,
// nested /Volumes slashes, metacharacters, empty — must be refused before any
// diskutil/hdiutil subprocess could read it.
func TestValidateVolumeIdentifier(t *testing.T) {
	accept := []string{
		"disk0", "disk2", "disk2s1", "disk10", "disk1s1s1",
		"/Volumes/Backup", "/Volumes/Macintosh HD", "/Volumes/My_Drive-2 (ext)",
	}
	for _, v := range accept {
		if got, err := validateVolumeIdentifier("op", "volume", v); err != nil {
			t.Errorf("validateVolumeIdentifier(%q): unexpected error %v", v, err)
		} else if got != v {
			t.Errorf("validateVolumeIdentifier(%q) = %q, want it returned verbatim", v, got)
		}
	}

	reject := []string{
		"",                       // empty
		"-disk0",                 // dash-leading (flag injection)
		"--force",                // a flag
		"disk",                   // no number
		"disk2s",                 // trailing slice with no number
		"/Volumes/../etc/passwd", // traversal via slash
		"/Volumes/..",            // single-component parent-dir traversal (normalizes to /)
		"/Volumes/.",             // single-component current-dir (normalizes to /Volumes)
		"/Volumes/a/b",           // extra path component
		"/etc/passwd",            // outside /Volumes
		"disk2; rm -rf /",        // metacharacters
		"/Volumes/x`reboot`",     // backtick
		"disk2 s1",               // space in a disk id
		"Backup",                 // bare name, not a disk id or /Volumes path
	}
	for _, v := range reject {
		if _, err := validateVolumeIdentifier("op", "volume", v); err == nil {
			t.Errorf("validateVolumeIdentifier(%q): expected rejection, got nil error", v)
		}
	}
}

// TestStorage_HostileIdentifierRejected is the per-operation injection
// regression: a dash-leading identifier must be refused up front by
// validateVolumeIdentifier for both identifier-taking read ops, never assembled
// into a diskutil argv where it could be read as a flag. Neither op runs a
// subprocess when the guard fires.
func TestStorage_HostileIdentifierRejected(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	ops := map[string]BuiltinFunc{
		"volume_info":  runVolumeInfo,
		"eject_volume": runEjectVolume,
	}
	for _, hostile := range []string{"-e", "-rf", "--flood", "-"} {
		for name, fn := range ops {
			if _, err := fn(ctx, cap, map[string]any{"volume": hostile}); err == nil {
				t.Errorf("%s(%q): expected rejection of a dash-leading identifier, got nil error", name, hostile)
			}
		}
	}
}

// TestEjectVolumeIsAdvisory confirms eject_volume never produces an eject
// command: the guardrail rejects a bad identifier without touching diskutil, and
// the operation is registered as a read-only builtin (so the server runs it
// immediately and never routes it through the stage/execute mutation path that
// could actually eject a disk).
func TestEjectVolumeIsAdvisory(t *testing.T) {
	if _, isBuiltin := builtins["eject_volume"]; !isBuiltin {
		t.Fatal("eject_volume must be a read-only builtin, not a mutator")
	}
	if _, isMutator := mutators["eject_volume"]; isMutator {
		t.Error("eject_volume must NOT be a mutator — it may never execute an eject")
	}
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	c, ok := reg.Lookup("eject_volume")
	if !ok {
		t.Fatal("eject_volume not in registry")
	}
	if c.Reversibility != registry.ReadOnly {
		t.Errorf("eject_volume reversibility = %q, want read_only (advisory, never executes)", c.Reversibility)
	}
}

// TestTmutilListOrExplain checks the "nothing to list" rendering: a successful
// non-empty result is returned verbatim, an empty/failed result falls back to the
// binary's stderr when present, and to the supplied fallback otherwise.
func TestTmutilListOrExplain(t *testing.T) {
	if got := tmutilListOrExplain(&runResult{Stdout: "/backup/2026-07-04", ExitCode: 0}, "fb"); got != "/backup/2026-07-04" {
		t.Errorf("success case = %q, want the stdout verbatim", got)
	}
	if got := tmutilListOrExplain(&runResult{Stderr: "No destinations configured", ExitCode: 1}, "fb"); got != "No destinations configured" {
		t.Errorf("failure-with-stderr case = %q, want the stderr", got)
	}
	if got := tmutilListOrExplain(&runResult{ExitCode: 1}, "fallback text"); got != "fallback text" {
		t.Errorf("failure-no-detail case = %q, want the fallback", got)
	}
}

// TestStorageBuiltins_Live exercises the real read binaries end-to-end. Skipped
// unless MCP_STORAGE_LIVE=1 because it shells out and its output depends on host
// state. These are all read-only and safe on any Mac.
func TestStorageBuiltins_Live(t *testing.T) {
	if os.Getenv("MCP_STORAGE_LIVE") != "1" {
		t.Skip("set MCP_STORAGE_LIVE=1 to run the live storage builtins")
	}
	ctx := context.Background()
	cap := registry.Capability{}

	if out, err := runTimeMachineStatus(ctx, cap, nil); err != nil {
		t.Errorf("runTimeMachineStatus: %v", err)
	} else if !strings.Contains(out, "Time Machine status") {
		t.Errorf("time_machine_status missing header, got: %s", out)
	}
	if out, err := runListVolumes(ctx, cap, nil); err != nil {
		t.Errorf("runListVolumes: %v", err)
	} else if !strings.Contains(out, "disk") {
		t.Errorf("list_volumes should mention at least one disk, got: %s", out)
	}
	// disk0 exists on every Mac; the advisory op must confirm it and hand back the
	// exact eject command WITHOUT running it.
	if out, err := runEjectVolume(ctx, cap, map[string]any{"volume": "disk0"}); err != nil {
		t.Errorf("runEjectVolume(disk0): %v", err)
	} else if !strings.Contains(out, "diskutil eject disk0") || !strings.Contains(out, "does NOT eject") {
		t.Errorf("eject_volume should advise, not eject; got: %s", out)
	}
}
