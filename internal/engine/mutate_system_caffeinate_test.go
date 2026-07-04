// mutate_system_caffeinate_test.go covers the keep-awake pair: keep_awake stages
// a detached, timed caffeinate with no undo and a bounded duration; allow_sleep
// can only ever SIGTERM caffeinate processes owned by the current user. Together
// with the detach round-trip in executor_test.go these pin the safety-relevant
// shape of the engine's one long-lived-helper capability.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// keepAwakeCap mirrors the manifest's keep_awake entry closely enough for the
// mutator, which reads only the normalized "duration_seconds".
var keepAwakeCap = registry.Capability{
	Name:          "keep_awake",
	Category:      "system",
	Binary:        "caffeinate",
	Reversibility: registry.Irreversible,
	Risk:          registry.RiskMedium,
	Builder:       "keep_awake",
	Params: []registry.ParamSpec{
		{Name: "duration_seconds", Type: registry.TypeInt, Default: float64(3600), Arg: registry.ArgRule{Kind: registry.ArgNone}},
	},
}

// TestStageKeepAwake_DefaultDuration confirms the default (1 hour) yields a
// detached `caffeinate -d -i -t 3600`, no inverse, and a preview that names the
// duration in words and points at allow_sleep.
func TestStageKeepAwake_DefaultDuration(t *testing.T) {
	plan, err := New().Stage(context.Background(), keepAwakeCap, map[string]any{})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	wantArgs := []string{"-d", "-i", "-t", "3600"}
	if plan.Forward.Binary != "caffeinate" || !equalStrings(plan.Forward.Args, wantArgs) {
		t.Errorf("forward = %q %v, want caffeinate %v", plan.Forward.Binary, plan.Forward.Args, wantArgs)
	}
	if !plan.Forward.Detach {
		t.Error("keep_awake forward command must be Detach=true so caffeinate outlives the call")
	}
	if plan.Inverse != nil {
		t.Errorf("keep_awake must have no inverse (irreversible), got %+v", plan.Inverse)
	}
	if !strings.Contains(plan.Preview, "1 hour") || !strings.Contains(plan.Preview, "allow_sleep") {
		t.Errorf("preview should name the duration and allow_sleep: %q", plan.Preview)
	}
}

// TestStageKeepAwake_CustomDuration pins the argv for a non-default duration and
// checks the humanized preview text (90 minutes -> "1 hour 30 minutes").
func TestStageKeepAwake_CustomDuration(t *testing.T) {
	plan, err := New().Stage(context.Background(), keepAwakeCap, map[string]any{"duration_seconds": float64(5400)})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if got := plan.Forward.Args[len(plan.Forward.Args)-1]; got != "5400" {
		t.Errorf("forward -t value = %q, want 5400", got)
	}
	if !strings.Contains(plan.Preview, "1 hour 30 minutes") {
		t.Errorf("preview should humanize 5400s as '1 hour 30 minutes': %q", plan.Preview)
	}
}

// TestStageKeepAwake_RejectsOutOfRange confirms durations below the 60s floor or
// above the 4-hour ceiling are refused before any command is built.
func TestStageKeepAwake_RejectsOutOfRange(t *testing.T) {
	for _, secs := range []int{0, 59, 14401, 100000} {
		if _, err := New().Stage(context.Background(), keepAwakeCap, map[string]any{"duration_seconds": float64(secs)}); err == nil {
			t.Errorf("duration %d should be rejected as out of range", secs)
		}
	}
}

// TestCaffeinatePIDs_OnlyCaffeinateOwnedByUser is the safety-critical filter
// test: given a hostile process snapshot mixing unrelated processes, other
// users' caffeinate, and a protected low PID, allow_sleep's target list contains
// ONLY the current user's caffeinate PIDs (> 1). This is what guarantees the
// operation can never SIGTERM anything but a keep-awake helper.
func TestCaffeinatePIDs_OnlyCaffeinateOwnedByUser(t *testing.T) {
	rows := []procRow{
		{pid: 1, user: "me", command: "/usr/bin/caffeinate"},                    // protected low PID
		{pid: 100, user: "me", command: "/usr/bin/caffeinate"},                  // KEEP
		{pid: 200, user: "root", command: "/usr/bin/caffeinate"},                // other user
		{pid: 300, user: "me", command: "/Applications/Evil.app/caffeinate.sh"}, // basename "caffeinate.sh" != "caffeinate"
		{pid: 400, user: "me", command: "/usr/sbin/decaffeinate"},               // name contains but != caffeinate
		{pid: 500, user: "me", command: "/usr/bin/caffeinate"},                  // KEEP
		{pid: 600, user: "me", command: "/bin/sleep"},                           // unrelated
	}
	got := caffeinatePIDs(rows, "me")
	want := []int{100, 500}
	if !equalInts(got, want) {
		t.Errorf("caffeinatePIDs = %v, want %v (only the current user's caffeinate, PID > 1)", got, want)
	}
}

// TestCaffeinatePIDs_EmptyUsernameMatchesByNameOnly confirms the "" username
// fallback still constrains to caffeinate (never widening to other processes),
// matching every caffeinate regardless of owner on a single-user Mac.
func TestCaffeinatePIDs_EmptyUsernameMatchesByNameOnly(t *testing.T) {
	rows := []procRow{
		{pid: 100, user: "me", command: "/usr/bin/caffeinate"},
		{pid: 200, user: "root", command: "/usr/bin/caffeinate"},
		{pid: 300, user: "me", command: "/bin/sleep"},
	}
	got := caffeinatePIDs(rows, "")
	want := []int{100, 200}
	if !equalInts(got, want) {
		t.Errorf("caffeinatePIDs(username=\"\") = %v, want %v", got, want)
	}
}

// TestKillCaffeinateCommand_IsAlwaysSigterm pins that allow_sleep's forward
// command hardcodes SIGTERM and lists exactly the given PIDs — never a
// force-kill (-KILL / -9) and never a stray operand.
func TestKillCaffeinateCommand_IsAlwaysSigterm(t *testing.T) {
	cmd := killCaffeinateCommand([]int{100, 500})
	if cmd.Binary != "kill" {
		t.Errorf("binary = %q, want kill", cmd.Binary)
	}
	want := []string{"-TERM", "100", "500"}
	if !equalStrings(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
	for _, a := range cmd.Args {
		if a == "-KILL" || a == "-9" || a == "-SIGKILL" {
			t.Errorf("allow_sleep must never force-kill; found %q in argv %v", a, cmd.Args)
		}
	}
}

// TestHumanizeSeconds covers the preview duration phrasing across the boundaries
// that matter (singular/plural, multi-component, sub-minute).
func TestHumanizeSeconds(t *testing.T) {
	cases := map[int]string{
		60:    "1 minute",
		90:    "1 minute 30 seconds",
		3600:  "1 hour",
		5400:  "1 hour 30 minutes",
		7200:  "2 hours",
		14400: "4 hours",
	}
	for secs, want := range cases {
		if got := humanizeSeconds(secs); got != want {
			t.Errorf("humanizeSeconds(%d) = %q, want %q", secs, got, want)
		}
	}
}

// equalInts reports element-wise slice equality for ints. (equalStrings lives in
// mutate_system_power_test.go, in the same package.)
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
