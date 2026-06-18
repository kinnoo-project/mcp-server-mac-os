// builtins_filesystem_test.go exercises the largest_files builtin end to end
// through the engine's Run pipeline against a hermetic tree of known-size files.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// largestFilesCapability loads the shipped largest_files capability so the test
// exercises the real manifest, not a hand-rolled copy.
func largestFilesCapability(t *testing.T) registry.Capability {
	t.Helper()
	r, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	c, ok := r.Lookup("largest_files")
	if !ok {
		t.Fatal("largest_files capability not found in registry")
	}
	return c
}

// makeSizedTree writes regular files of distinct, known sizes across nested
// subdirectories and returns the root.
func makeSizedTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]int{
		"small.txt":            100,
		"medium.txt":           5000,
		"nested/big.bin":       20000,
		"nested/tiny.txt":      10,
		"nested/deep/huge.dat": 50000,
	}
	for rel, size := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// rankLines extracts the numbered "N. <size> <path>" lines from the output,
// skipping the header and the coverage footer.
func rankLines(out string) []string {
	var lines []string
	for _, ln := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(ln)
		if len(trimmed) > 0 && trimmed[0] >= '0' && trimmed[0] <= '9' && strings.Contains(trimmed, ". ") {
			lines = append(lines, ln)
		}
	}
	return lines
}

// TestRun_LargestFiles_RanksTopN confirms the builtin returns exactly the N
// largest regular files, in descending order, excluding everything smaller.
func TestRun_LargestFiles_RanksTopN(t *testing.T) {
	root := makeSizedTree(t)
	out, err := New().Run(context.Background(), largestFilesCapability(t), map[string]any{
		"dir":   root,
		"count": float64(3),
	})
	if err != nil {
		t.Fatalf("Run largest_files: %v", err)
	}

	lines := rankLines(out)
	if len(lines) != 3 {
		t.Fatalf("expected 3 ranked files, got %d:\n%s", len(lines), out)
	}
	for i, want := range []string{"huge.dat", "big.bin", "medium.txt"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("rank %d = %q, want it to contain %q", i+1, lines[i], want)
		}
	}
	for _, excluded := range []string{"small.txt", "tiny.txt"} {
		if strings.Contains(out, excluded) {
			t.Errorf("output should exclude %q (below the top 3):\n%s", excluded, out)
		}
	}
}

// TestRun_LargestFiles_DefaultCount confirms count defaults to 10 and that a tree
// with fewer files simply returns all of them (no error, no padding).
func TestRun_LargestFiles_DefaultCount(t *testing.T) {
	root := makeSizedTree(t)
	out, err := New().Run(context.Background(), largestFilesCapability(t), map[string]any{"dir": root})
	if err != nil {
		t.Fatalf("Run largest_files: %v", err)
	}
	if got := len(rankLines(out)); got != 5 {
		t.Errorf("expected all 5 files ranked, got %d:\n%s", got, out)
	}
}

// TestRun_LargestFiles_NotADirectory confirms a non-directory target is a clear
// error rather than an empty or misleading result.
func TestRun_LargestFiles_NotADirectory(t *testing.T) {
	root := makeSizedTree(t)
	_, err := New().Run(context.Background(), largestFilesCapability(t), map[string]any{
		"dir": filepath.Join(root, "small.txt"),
	})
	if err == nil {
		t.Fatal("expected an error when dir is a file, got nil")
	}
}

// TestFormatBytes pins the human-readable size rendering at unit boundaries.
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0K"},
		{1536, "1.5K"},
		{5 * 1024 * 1024, "5.0M"},
		{3 * 1024 * 1024 * 1024, "3.0G"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
