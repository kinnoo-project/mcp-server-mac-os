// executor_test.go covers the subprocess layer's pure helpers: truncation,
// result rendering, and tilde expansion. The exec path itself is exercised
// end-to-end through engine_test.go.
package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompactOutput confirms short output is untouched and long output shrinks
// with a truncation notice.
func TestCompactOutput(t *testing.T) {
	short := strings.Repeat("a", 100)
	if got := compactOutput(short); got != short {
		t.Error("short output should be returned unchanged")
	}
	long := strings.Repeat("x", maxOutputBytes*3)
	got := compactOutput(long)
	if !strings.Contains(got, "truncated") {
		t.Error("long output should include a truncation notice")
	}
	if len(got) >= len(long) {
		t.Error("long output should actually shrink")
	}
}

// TestFormatRunResult checks stdout/stderr/exit-code rendering, including the
// empty-output sentinel.
func TestFormatRunResult(t *testing.T) {
	if got := formatRunResult(&runResult{Stdout: "hello"}); got != "hello" {
		t.Errorf("stdout-only render = %q, want %q", got, "hello")
	}
	got := formatRunResult(&runResult{Stderr: "boom", ExitCode: 1})
	if !strings.Contains(got, "[stderr]") || !strings.Contains(got, "boom") {
		t.Errorf("stderr should be flagged and preserved: %q", got)
	}
	if !strings.Contains(got, "exit code: 1") {
		t.Errorf("non-zero exit should be annotated: %q", got)
	}
	if got := formatRunResult(&runResult{}); got != "(no output)" {
		t.Errorf("empty result = %q, want %q", got, "(no output)")
	}
}

// TestExpandUserPath verifies tilde handling and pass-through of all other paths.
func TestExpandUserPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cases := []struct{ in, want string }{
		{"", ""},
		{"~", home},
		{"~/Downloads", filepath.Join(home, "Downloads")},
		{"/etc/hosts", "/etc/hosts"},
		{"./relative", "./relative"},
	}
	for _, c := range cases {
		got, err := expandUserPath(c.in)
		if err != nil {
			t.Fatalf("expandUserPath(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("expandUserPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
