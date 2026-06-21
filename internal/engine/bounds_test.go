// bounds_test.go is part of the production security gate (see docs/TESTS.md). It
// asserts the resource-exhaustion guardrails that keep a single tool call from
// becoming a denial-of-service: integer parameters are bounded (so a typo or a
// hostile prompt cannot ask for a runaway print job or pull the entire message
// store), non-integer values are rejected outright, and verbose command output
// is truncated so it cannot saturate the model's context window.
//
// A known gap is documented here as a skipped placeholder: the main subprocess
// path has no wall-clock timeout, so a long-running read (e.g. find over /) is
// bounded only by client cancellation. Fixing that is deferred to a follow-up
// hardening PR (see docs/issues/issue-no-executor-walltime-timeout.md); the
// skipped test names the place the fix's regression coverage will live.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// TestBounds_PrintCopiesRejectsOutOfRange confirms the copies parameter is
// rejected (not silently clamped) outside 1..maxPrintCopies, so a request for
// thousands of copies fails at stage time rather than spooling a runaway job.
func TestBounds_PrintCopiesRejectsOutOfRange(t *testing.T) {
	reject := []int{0, -1, maxPrintCopies + 1, 1000}
	for _, n := range reject {
		if _, err := validateCopies(map[string]any{"copies": n}); err == nil {
			t.Errorf("validateCopies(%d) = nil error, want rejection (allowed range is 1..%d)", n, maxPrintCopies)
		}
	}
	accept := []int{1, maxPrintCopies}
	for _, n := range accept {
		if got, err := validateCopies(map[string]any{"copies": n}); err != nil || got != n {
			t.Errorf("validateCopies(%d) = (%d, %v), want (%d, nil)", n, got, err, n)
		}
	}
	// Omitted copies defaults to a single copy.
	if got, err := validateCopies(map[string]any{}); err != nil || got != 1 {
		t.Errorf("validateCopies(absent) = (%d, %v), want (1, nil)", got, err)
	}
}

// TestBounds_MessageLimitIsCapped confirms the per-read message limit is clamped
// to a hard ceiling, so neither a huge limit nor a nonsensical one can pull an
// unbounded number of rows out of the Messages database into the response.
func TestBounds_MessageLimitIsCapped(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want int
	}{
		{"absent uses default", map[string]any{}, defaultMessageLimit},
		{"zero uses default", map[string]any{"limit": 0}, defaultMessageLimit},
		{"negative uses default", map[string]any{"limit": -10}, defaultMessageLimit},
		{"in range kept", map[string]any{"limit": 5}, 5},
		{"over ceiling clamped", map[string]any{"limit": 100000}, maxMessageLimit},
		{"exactly ceiling kept", map[string]any{"limit": maxMessageLimit}, maxMessageLimit},
	}
	for _, tc := range cases {
		if got := cappedLimit(tc.in, defaultMessageLimit); got != tc.want {
			t.Errorf("%s: cappedLimit = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestBounds_IntParamRejectsNonInteger confirms the validator refuses a
// fractional value where an integer is required. JSON numbers decode to float64,
// so without this check a value like 3.14 (or a deliberately odd 1e9) could slip
// through; rejecting it keeps every bounded counter strictly whole.
func TestBounds_IntParamRejectsNonInteger(t *testing.T) {
	c := registry.Capability{
		Name:   "sweep_int",
		Params: []registry.ParamSpec{{Name: "count", Type: registry.TypeInt}},
	}
	for _, bad := range []any{3.14, 0.5, "10", true} {
		if _, err := normalizeParams(c, map[string]any{"count": bad}); err == nil {
			t.Errorf("normalizeParams with count=%v (%T) = nil error, want rejection", bad, bad)
		}
	}
	// A whole-number float (how JSON delivers integers) is accepted.
	if _, err := normalizeParams(c, map[string]any{"count": float64(7)}); err != nil {
		t.Errorf("normalizeParams with whole-number count = %v, want acceptance", err)
	}
}

// TestBounds_OutputIsTruncated confirms output beyond the byte budget is
// compacted to a head/tail window with an explicit "bytes truncated" notice, so
// a multi-megabyte command output cannot flood the model's context window.
func TestBounds_OutputIsTruncated(t *testing.T) {
	// Short output passes through untouched.
	short := strings.Repeat("a", maxOutputBytes)
	if got := compactOutput(short); got != short {
		t.Errorf("output at the budget was altered; compaction should be a no-op until it is exceeded")
	}

	// Oversized output is trimmed and annotated.
	big := strings.Repeat("a", maxOutputBytes*3)
	got := compactOutput(big)
	if len(got) >= len(big) {
		t.Errorf("oversized output was not truncated: got %d bytes, input was %d", len(got), len(big))
	}
	if !strings.Contains(got, "bytes truncated") {
		t.Errorf("truncated output missing the explanatory notice, got: %.120q...", got)
	}
	if !strings.HasPrefix(got, "aaaa") || !strings.HasSuffix(got, "aaaa") {
		t.Errorf("truncated output should retain a head and a tail of the original")
	}
}

// TestBounds_CopyFits checks the pure fit decision: a source fits only when it
// plus the reserved headroom stays within the available space.
func TestBounds_CopyFits(t *testing.T) {
	cases := []struct {
		name       string
		size, free int64
		want       bool
	}{
		{"tiny on huge", 1 << 10, 100 << 30, true},
		{"exactly fits with headroom", 1 << 30, (1 << 30) + copyHeadroomBytes, true},
		{"one byte over", (1 << 30) + 1, (1 << 30) + copyHeadroomBytes, false},
		{"no room for headroom", 10, copyHeadroomBytes, false},
		{"empty source still needs headroom", 0, copyHeadroomBytes - 1, false},
	}
	for _, tc := range cases {
		if got := copyFits(tc.size, tc.free); got != tc.want {
			t.Errorf("%s: copyFits(%d, %d) = %v, want %v", tc.name, tc.size, tc.free, got, tc.want)
		}
	}
}

// TestBounds_TreeSizeBytes confirms the source-size estimate sums regular-file
// sizes across a tree and ignores directories.
func TestBounds_TreeSizeBytes(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileForTest(t, filepath.Join(dir, "a.bin"), strings.Repeat("x", 1000))
	writeFileForTest(t, filepath.Join(sub, "b.bin"), strings.Repeat("y", 2500))

	got, err := treeSizeBytes(context.Background(), dir)
	if err != nil {
		t.Fatalf("treeSizeBytes: %v", err)
	}
	if got != 3500 {
		t.Errorf("treeSizeBytes = %d, want 3500 (1000 + 2500)", got)
	}
}

// TestBounds_ExecutorWallClockTimeout confirms the shared subprocess path kills a
// command that overruns the wall-clock budget, turning a would-be time/CPU DoS
// into a prompt, clearly-labeled error. The budget is lowered for the duration of
// the test so it finishes in a fraction of a second rather than the production
// two-minute ceiling.
func TestBounds_ExecutorWallClockTimeout(t *testing.T) {
	original := commandTimeout
	commandTimeout = 200 * time.Millisecond
	t.Cleanup(func() { commandTimeout = original })

	bin, err := policy.ResolveBinary("sleep")
	if err != nil {
		t.Fatalf("resolving sleep: %v", err)
	}

	start := time.Now()
	_, err = runCommand(context.Background(), bin, "10")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error for a command exceeding the budget, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should report a timeout, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("command ran for %s; the timeout should have killed it near 200ms", elapsed)
	}
}
