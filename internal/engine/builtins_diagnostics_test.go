// builtins_diagnostics_test.go tests the pure validation, predicate-composition,
// and parsing helpers behind the diagnostics builtins against synthetic input —
// no live `log`, `top`, or `pmset` calls except in the explicitly gated live
// test. The centrepiece is TestComposeLogPredicate, the mandatory
// option/predicate-injection regression for system_log: a hostile process or
// subsystem value must be rejected before it can reach the composed predicate,
// never smuggled in as syntax that escapes the quoted string it sits inside.
package engine

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestComposeLogPredicate is the injection regression for system_log. It proves
// two things: (1) legitimate process/subsystem values compose into the exact
// `field == "value"` predicate the query expects, and (2) every hostile value —
// a quote or backslash that could break out of the quoted string, a leading dash,
// a control character, a bad subsystem charset — is rejected outright rather than
// reaching the predicate. Because the predicate is the only place a model string
// influences the `log` argv, this table is the guard that keeps system_log safe.
func TestComposeLogPredicate(t *testing.T) {
	accept := []struct {
		name      string
		process   string
		subsystem string
		want      string
	}{
		{"neither", "", "", ""},
		{"process only", "Safari", "", `process == "Safari"`},
		{"process with space", "Google Chrome", "", `process == "Google Chrome"`},
		{"subsystem only", "", "com.apple.wifi", `subsystem == "com.apple.wifi"`},
		{"both", "bluetoothd", "com.apple.bluetooth", `process == "bluetoothd" && subsystem == "com.apple.bluetooth"`},
		{"whitespace trimmed", "  loginwindow  ", "", `process == "loginwindow"`},
	}
	for _, tc := range accept {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			got, err := composeLogPredicate(tc.process, tc.subsystem)
			if err != nil {
				t.Fatalf("composeLogPredicate(%q,%q) unexpected error: %v", tc.process, tc.subsystem, err)
			}
			if got != tc.want {
				t.Errorf("composeLogPredicate(%q,%q) = %q, want %q", tc.process, tc.subsystem, got, tc.want)
			}
		})
	}

	// A hostile PROCESS value must be rejected. `-e` proves the leading-dash guard;
	// the quote/backslash payloads prove the value cannot close its own predicate
	// string and inject further syntax.
	hostileProcess := []string{
		`-e`,
		`Safari" OR 1==1`,
		`x"y`,
		`x\y`,
		"x\ny",
		"x\x00y",
	}
	for _, h := range hostileProcess {
		// strconv.Quote keeps the subtest name readable and stable when the value
		// contains a newline or NUL, while the assertion still checks the raw value.
		t.Run("reject_process/"+strconv.Quote(h), func(t *testing.T) {
			if _, err := composeLogPredicate(h, ""); err == nil {
				t.Errorf("composeLogPredicate(process=%q) = nil error, want rejection", h)
			}
		})
	}

	// A hostile SUBSYSTEM value must be rejected by the reverse-DNS charset
	// allowlist (spaces, quotes, dashes-at-start, predicate operators, etc.).
	hostileSubsystem := []string{
		`-e`,
		`com.apple.wifi" OR 1==1`,
		`com apple wifi`,
		`com.apple/wifi`,
		"a\nb",
	}
	for _, h := range hostileSubsystem {
		t.Run("reject_subsystem/"+strconv.Quote(h), func(t *testing.T) {
			if _, err := composeLogPredicate("", h); err == nil {
				t.Errorf("composeLogPredicate(subsystem=%q) = nil error, want rejection", h)
			}
		})
	}
}

// TestParseTopSample checks that the top parser reads the SECOND sample (the one
// with real interval CPU figures), tolerates the two-sample banner layout, and
// splits each row into pid / cpu / mem / command with the command (which may
// contain spaces) taken as the trailing remainder.
func TestParseTopSample(t *testing.T) {
	// Two samples, mirroring `top -l 2 ... -stats pid,cpu,mem,command`. The first
	// sample's WindowServer shows a bogus since-boot CPU (99.9) that must NOT be
	// what we read; the second sample is authoritative.
	sample := strings.Join([]string{
		"Processes: 605 total, 2 running, 603 sleeping",
		"2026/07/04 15:00:00",
		"Load Avg: 2.00, 1.80, 1.50",
		"PID    %CPU MEM   COMMAND",
		"1234   99.9 456M  WindowServer",
		"",
		"Processes: 605 total, 3 running, 602 sleeping",
		"2026/07/04 15:00:01",
		"Load Avg: 2.10, 1.82, 1.51",
		"PID    %CPU MEM    COMMAND",
		"1234   12.3 456M   WindowServer",
		"9876   4.5  1024M  Google Chrome Helper (Renderer)",
		"5     0.0  8K     kernel_task",
	}, "\n")

	rows := parseTopSample(sample)
	if len(rows) != 3 {
		t.Fatalf("parseTopSample returned %d rows, want 3: %+v", len(rows), rows)
	}
	// Must come from the SECOND sample: WindowServer's CPU is 12.3, not 99.9.
	if rows[0].pid != "1234" || rows[0].cpu != "12.3" || rows[0].mem != "456M" || rows[0].command != "WindowServer" {
		t.Errorf("row0 = %+v, want pid=1234 cpu=12.3 mem=456M command=WindowServer", rows[0])
	}
	// Command with spaces must be preserved verbatim as the trailing remainder.
	if rows[1].command != "Google Chrome Helper (Renderer)" {
		t.Errorf("row1 command = %q, want the full space-bearing name", rows[1].command)
	}
	if rows[2].pid != "5" || rows[2].command != "kernel_task" {
		t.Errorf("row2 = %+v, want pid=5 command=kernel_task", rows[2])
	}
}

// TestParseTopSample_NoTable returns nil when there is no parseable process
// table (e.g. truncated or unexpected output), so the caller reports an empty
// result rather than crashing.
func TestParseTopSample_NoTable(t *testing.T) {
	if rows := parseTopSample("garbage\nwith no header\n"); rows != nil {
		t.Errorf("parseTopSample(no header) = %+v, want nil", rows)
	}
}

// TestRenderThermalState covers the three interpretations: an explicit
// CPU_Speed_Limit below 100 (throttled), a limit of 100 (full speed), and the
// "no thermal warning level" wording pmset prints on a cool machine.
func TestRenderThermalState(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // a substring the verdict must contain
	}{
		{"throttled", "CPU_Speed_Limit \t= 80\nCPU_Available_CPUs = 8", "throttled to 80%"},
		{"full speed", "CPU_Speed_Limit = 100\nCPU_Available_CPUs = 8", "full speed"},
		{"no warning recorded", "Note: No thermal warning level has been recorded", "No thermal pressure"},
		{"unrecognized wording still gets a verdict", "Some_Other_Field = 42\nCPU_Scheduler_Limit = 100", "No explicit throttling indicator"},
		{"empty", "", "No thermal information"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderThermalState(tc.in)
			if !strings.Contains(got, tc.want) {
				t.Errorf("renderThermalState(%q) = %q, want it to contain %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSystemLog_RejectsHostileFilter is the end-to-end guard proving the builtin
// itself (not just composeLogPredicate) refuses a hostile process value before
// spawning anything — a `-e` filter must yield an error, never a log query.
func TestSystemLog_RejectsHostileFilter(t *testing.T) {
	_, err := runSystemLog(context.Background(), registry.Capability{}, map[string]any{"process": "-e"})
	if err == nil {
		t.Fatal("runSystemLog with a dash-leading process filter should error, got nil")
	}
}

// TestDiagnosticsBuiltins_Live exercises the three builtins end-to-end against
// the real machine. Skipped unless MCP_DIAGNOSTICS_LIVE=1, since output depends
// on the host's live state and shells out to real binaries — mirroring the
// network domain's live-gated test.
func TestDiagnosticsBuiltins_Live(t *testing.T) {
	if os.Getenv("MCP_DIAGNOSTICS_LIVE") != "1" {
		t.Skip("set MCP_DIAGNOSTICS_LIVE=1 to run the live diagnostics builtins")
	}
	ctx := context.Background()
	cap := registry.Capability{}

	if out, err := runSystemLog(ctx, cap, map[string]any{"duration_minutes": 1}); err != nil {
		t.Errorf("runSystemLog: %v", err)
	} else if !strings.Contains(out, "Unified system log") {
		t.Errorf("system_log missing header, got: %s", out[:min(200, len(out))])
	}
	if out, err := runTopProcesses(ctx, cap, map[string]any{"limit": 5}); err != nil {
		t.Errorf("runTopProcesses: %v", err)
	} else if !strings.Contains(out, "COMMAND") {
		t.Errorf("top_processes missing table header, got: %s", out)
	}
	if out, err := runThermalState(ctx, cap, nil); err != nil {
		t.Errorf("runThermalState: %v", err)
	} else if !strings.Contains(out, "Thermal state") {
		t.Errorf("thermal_state missing header, got: %s", out)
	}
}
