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
	"strings"
	"testing"

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

// TestBounds_ExecutorWallClockTimeout is a placeholder for the deferred hardening
// fix: the shared subprocess path (execCommand) currently binds only to the
// request context, so a long-running read is not independently time-bounded. When
// the timeout is added (see docs/issues/issue-no-executor-walltime-timeout.md),
// un-skip this test and assert a command exceeding the budget is killed.
func TestBounds_ExecutorWallClockTimeout(t *testing.T) {
	t.Skip("deferred to hardening PR: execCommand has no wall-clock timeout yet (docs/issues/issue-no-executor-walltime-timeout.md)")
}
