// binaries_test.go verifies the trust boundary: resolution succeeds for genuine
// system tools and is rejected for path-bearing or out-of-tree names.
package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveBinary_AllowedDirs confirms a real system utility resolves to a
// path under a trusted directory.
func TestResolveBinary_AllowedDirs(t *testing.T) {
	abs, err := ResolveBinary("ls")
	if err != nil {
		t.Fatalf("ResolveBinary(ls): %v", err)
	}
	ok := false
	for _, d := range allowedBinDirs {
		if strings.HasPrefix(abs, d+"/") {
			ok = true
			break
		}
	}
	if !ok {
		t.Errorf("resolved %q is not under a trusted directory %v", abs, allowedBinDirs)
	}
}

// TestResolveBinary_RejectsPathSeparators confirms callers cannot smuggle a
// path (and thus an arbitrary executable) through the bare-name contract.
func TestResolveBinary_RejectsPathSeparators(t *testing.T) {
	for _, bad := range []string{"../evil", "/usr/bin/ls", "sub/dir"} {
		if _, err := ResolveBinary(bad); err == nil {
			t.Errorf("expected rejection for name with separator %q", bad)
		}
	}
}

// TestResolveBinary_RejectsUnknown confirms a name that exists nowhere trusted
// is rejected rather than silently resolved.
func TestResolveBinary_RejectsUnknown(t *testing.T) {
	if _, err := ResolveBinary("definitely-not-a-real-binary-xyzzy"); err == nil {
		t.Fatal("expected error for unknown binary")
	}
}

// TestResolveBinary_IgnoresRogueOnPath confirms that a binary planted earlier on
// $PATH (a rogue substitute, or a foreign tool from an SDK directory — the exact
// case that broke CI, where an Android SDK "sqlite3" preceded /usr/bin) is NOT
// returned: resolution falls back to the genuine system tool under a trusted
// directory, and never the planted one.
func TestResolveBinary_IgnoresRogueOnPath(t *testing.T) {
	rogueDir := t.TempDir()
	rogue := filepath.Join(rogueDir, "ls")
	if err := os.WriteFile(rogue, []byte("#!/bin/sh\necho pwned\n"), 0o755); err != nil {
		t.Fatalf("planting rogue binary: %v", err)
	}
	// Put the rogue directory first so a naive LookPath would return it.
	t.Setenv("PATH", rogueDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	abs, err := ResolveBinary("ls")
	if err != nil {
		t.Fatalf("ResolveBinary(ls) with a rogue on PATH should fall back, got: %v", err)
	}
	if abs == rogue {
		t.Fatalf("ResolveBinary returned the rogue binary %q; it must never be selected", abs)
	}
	if !underTrustedDir(abs, "ls") {
		t.Errorf("resolved %q is not under a trusted directory %v", abs, allowedBinDirs)
	}
}
