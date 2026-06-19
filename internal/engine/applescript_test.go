// applescript_test.go tests the pure, subprocess-free helpers that underpin
// every osascript-backed capability: the "--"-terminator hardening, date/time
// parsing and round-tripping, and row/field splitting. None of these launch
// osascript or touch Calendar/Reminders.
package engine

import (
	"reflect"
	"testing"
)

// TestOsascriptCommand_InsertsTerminator is the structural guarantee behind the
// option-injection hardening: every osascript Command must place "--" between
// the script and the first data argument, so a value beginning with "-" can
// never be parsed as an osascript flag.
func TestOsascriptCommand_InsertsTerminator(t *testing.T) {
	cmd := osascriptCommand("SCRIPT", "-e", "second")
	want := []string{"-e", "SCRIPT", "--", "-e", "second"}
	if cmd.Binary != "osascript" || !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("osascriptCommand built %s %v, want osascript %v", cmd.Binary, cmd.Args, want)
	}
}

func TestParseDate(t *testing.T) {
	y, m, d, err := parseDate("2026-06-18")
	if err != nil || y != 2026 || m != 6 || d != 18 {
		t.Fatalf("parseDate(2026-06-18) = %d-%d-%d, %v", y, m, d, err)
	}
	for _, bad := range []string{"2026-13-01", "2026-02-30", "06/18/2026", "", "2026-6-18T00:00"} {
		if _, _, _, err := parseDate(bad); err == nil {
			t.Errorf("parseDate(%q) should have failed", bad)
		}
	}
}

func TestParseClock(t *testing.T) {
	h, m, err := parseClock("09:05")
	if err != nil || h != 9 || m != 5 {
		t.Fatalf("parseClock(09:05) = %d:%d, %v", h, m, err)
	}
	for _, bad := range []string{"24:00", "9:5", "25:61", "noon", ""} {
		if _, _, err := parseClock(bad); err == nil {
			t.Errorf("parseClock(%q) should have failed", bad)
		}
	}
}

// TestParseFmtDateTime_RoundTrips confirms the "YYYY-MM-DD HH:MM" a probe script
// emits parses back to exactly the components needed to rebuild it — the basis
// for restoring a captured prior start/end or due date on undo.
func TestParseFmtDateTime_RoundTrips(t *testing.T) {
	got, err := parseFmtDateTime("2026-12-31 23:59")
	if err != nil {
		t.Fatalf("parseFmtDateTime: %v", err)
	}
	want := ymdhm{2026, 12, 31, 23, 59}
	if got != want {
		t.Errorf("parseFmtDateTime = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(got.args(), []string{"2026", "12", "31", "23", "59"}) {
		t.Errorf("args() = %v", got.args())
	}
	if s := fmtYMDHM(want); s != "2026-12-31 23:59" {
		t.Errorf("fmtYMDHM round-trip = %q", s)
	}
}

func TestParseFmtDateTime_Rejects(t *testing.T) {
	for _, bad := range []string{"2026-12-31", "garbage", ""} {
		if _, err := parseFmtDateTime(bad); err == nil {
			t.Errorf("parseFmtDateTime(%q) should have failed", bad)
		}
	}
}

func TestAsRows(t *testing.T) {
	if got := asRows("\n  \n"); got != nil {
		t.Errorf("blank input should yield nil, got %v", got)
	}
	got := asRows("a\tb\nc\td\n")
	want := []string{"a\tb", "c\td"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("asRows = %v, want %v", got, want)
	}
}
