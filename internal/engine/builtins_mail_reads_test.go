// builtins_mail_reads_test.go tests the pure, in-Go logic of the Mail read
// builtins (list_inbox, read_message): parsing the AppleScript scripts' delimited
// stdout, ordering messages newest-first, capping output, rendering, and the
// required-parameter guard. The osascript "--"-terminator hardening that guards
// the mailbox/id inputs is the SAME runOsascript seam exercised by
// TestOsascriptArgv_SharedByBothPaths (applescript_test.go) and the shared
// injection sweep (injection_sweep_test.go), so it is not re-driven here.
//
// SAFETY: nothing here launches osascript or touches the real Mail app — these
// tests feed the parser/renderer the kind of rows the scripts emit, so they read
// no real mail and require no Automation grant.
package engine

import (
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestParseInboxRows verifies well-formed tab-delimited rows are parsed and that
// a row with the wrong field count is skipped rather than corrupting the result.
func TestParseInboxRows(t *testing.T) {
	stdout := strings.Join([]string{
		"101\tJane Doe <jane@example.com>\tLunch tomorrow?\t2026-06-19 09:30",
		"malformed-row-without-tabs",
		"102\tBilling <billing@example.com>\tYour receipt\t2026-06-20 14:05",
		"", // blank line dropped by asRows
	}, "\n")

	got := parseInboxRows(stdout)
	if len(got) != 2 {
		t.Fatalf("expected 2 parsed rows (malformed skipped), got %d: %+v", len(got), got)
	}
	if got[0].id != "101" || got[0].sender != "Jane Doe <jane@example.com>" ||
		got[0].subject != "Lunch tomorrow?" || got[0].received != "2026-06-19 09:30" {
		t.Errorf("first row parsed wrong: %+v", got[0])
	}
}

// TestSortInboxNewestFirst verifies messages come back most-recently-received
// first (a descending lexicographic sort of the "YYYY-MM-DD HH:MM" field is a
// descending time sort).
func TestSortInboxNewestFirst(t *testing.T) {
	msgs := []inboxMsg{
		{id: "a", received: "2026-01-01 08:00"},
		{id: "b", received: "2026-06-20 14:05"},
		{id: "c", received: "2026-03-15 12:00"},
	}
	got := sortInboxNewestFirst(msgs)
	if got[0].id != "b" || got[1].id != "c" || got[2].id != "a" {
		t.Errorf("expected newest-first [b c a], got [%s %s %s]", got[0].id, got[1].id, got[2].id)
	}
}

// TestRenderInboxList verifies the listing exposes each message's id (what
// read_message consumes) and uses placeholders for empty sender/subject fields.
func TestRenderInboxList(t *testing.T) {
	out := renderInboxList("1 message(s):", []inboxMsg{
		{id: "101", sender: "", subject: "", received: "2026-06-20 14:05"},
	})
	for _, want := range []string{"id: 101", "(unknown)", "(untitled)", "2026-06-20 14:05"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered list missing %q:\n%s", want, out)
		}
	}
}

// TestCappedInboxLimit covers the default, the hard ceiling, and a non-positive
// value falling back to the default.
func TestCappedInboxLimit(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want int
	}{
		{map[string]any{}, defaultInboxLimit},
		{map[string]any{"limit": 10}, 10},
		{map[string]any{"limit": 999}, maxInboxLimit},
		{map[string]any{"limit": 0}, defaultInboxLimit},
		{map[string]any{"limit": -5}, defaultInboxLimit},
	}
	for _, c := range cases {
		if got := cappedInboxLimit(c.in); got != c.want {
			t.Errorf("cappedInboxLimit(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestReadMessage_RequiresID verifies read_message refuses an empty id before it
// would ever reach osascript, so a missing id is a clear caller error rather
// than an opaque AppleScript failure.
func TestReadMessage_RequiresID(t *testing.T) {
	if _, err := runReadMessage(nil, readMessageCapability(t), map[string]any{}); err == nil {
		t.Fatal("expected an error when 'id' is missing")
	}
	if _, err := runReadMessage(nil, readMessageCapability(t), map[string]any{"id": "   "}); err == nil {
		t.Fatal("expected an error when 'id' is blank")
	}
}

// readMessageCapability loads the shipped read_message capability so the test
// exercises the real registered manifest, not a hand-built stand-in.
func readMessageCapability(t *testing.T) registry.Capability {
	return lookupCapability(t, "read_message")
}
