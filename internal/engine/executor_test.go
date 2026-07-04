// executor_test.go covers the subprocess layer's pure helpers: truncation,
// result rendering, and tilde expansion. The exec path itself is exercised
// end-to-end through engine_test.go.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestExecDetached_ReturnsImmediatelyAndOutlivesRequest proves the detach path:
// a long-lived child (`sleep`) is started in the background, RunCommand returns
// right away naming its PID, and — crucially — the child keeps running after the
// call returns rather than being killed with the request. It is cleaned up with
// a SIGTERM at the end so the test leaves nothing behind.
func TestExecDetached_ReturnsImmediatelyAndOutlivesRequest(t *testing.T) {
	start := time.Now()
	out, err := New().RunCommand(context.Background(), Command{
		Binary: "sleep",
		Args:   []string{"30"},
		Detach: true,
	})
	if err != nil {
		t.Fatalf("RunCommand(detached): %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("detached command should return immediately, took %s", elapsed)
	}
	if !strings.Contains(out, "PID") {
		t.Errorf("detached result should report the PID, got %q", out)
	}

	pid := parsePIDFromDetachOutput(t, out)
	// The process must still be alive: signal 0 checks existence without killing.
	if err := syscall.Kill(pid, 0); err != nil {
		t.Errorf("detached child (pid %d) should still be running, signal-0 err: %v", pid, err)
	}
	// Clean up so the background sleep does not linger.
	_ = syscall.Kill(pid, syscall.SIGTERM)
}

// TestExecDetached_HonoursCancelledContext confirms a cancelled request starts
// nothing on the detach path, exactly as the waiting path does.
func TestExecDetached_HonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().RunCommand(ctx, Command{Binary: "sleep", Args: []string{"30"}, Detach: true}); err == nil {
		t.Fatal("expected a cancelled context to start nothing on the detach path")
	}
}

// parsePIDFromDetachOutput extracts the integer PID from execDetached's
// "... (PID 1234)." line.
func parsePIDFromDetachOutput(t *testing.T, out string) int {
	t.Helper()
	i := strings.LastIndex(out, "PID ")
	if i < 0 {
		t.Fatalf("no PID in detach output %q", out)
	}
	rest := out[i+len("PID "):]
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(rest)
	}
	pid := 0
	for _, r := range rest[:end] {
		pid = pid*10 + int(r-'0')
	}
	if pid <= 1 {
		t.Fatalf("parsed implausible pid %d from %q", pid, out)
	}
	return pid
}

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
