// builtins_phone_test.go tests find_contact's pure parsing/rendering logic and
// label cleanup against canned Contacts-script output — no subprocess, no real
// Contacts data. The one live path (resolveContactNumbers → osascript) is not
// exercised here; only the required-name guard, which fails before any
// subprocess, is checked.
package engine

import (
	"context"
	"strings"
	"testing"
)

func TestFriendlyLabel(t *testing.T) {
	cases := map[string]string{
		"_$!<Mobile>!$_":     "Mobile",
		"_$!<Home>!$_":       "Home",
		"Work":               "Work",
		"":                   "",
		"  _$!<iPhone>!$_  ": "iPhone",
	}
	for raw, want := range cases {
		if got := friendlyLabel(raw); got != want {
			t.Errorf("friendlyLabel(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseContactRows(t *testing.T) {
	stdout := "Alice Example\t_$!<Mobile>!$_\t+1 555-111-2222\n" +
		"Alice Example\t_$!<Home>!$_\t555-333-4444\n" +
		"malformed-row-no-tabs\n" +
		"Bob Sansnumber\t_$!<Mobile>!$_\t\n" + // empty number → skipped
		"Carol Roe\tWork\t+1 555-999-0000\n"
	got := parseContactRows(stdout)
	if len(got) != 3 {
		t.Fatalf("expected 3 valid rows, got %d: %+v", len(got), got)
	}
	if got[0] != (contactPhone{name: "Alice Example", label: "Mobile", number: "+1 555-111-2222"}) {
		t.Errorf("row 0 = %+v", got[0])
	}
	if got[2].name != "Carol Roe" || got[2].label != "Work" {
		t.Errorf("row 2 = %+v", got[2])
	}
}

func TestRenderContacts(t *testing.T) {
	contacts := []contactPhone{
		{name: "Alice Example", label: "Mobile", number: "+1 555-111-2222"},
		{name: "Alice Example", label: "Home", number: "555-333-4444"},
		{name: "Carol Roe", label: "Work", number: "+1 555-999-0000"},
	}
	out := renderContacts("Ali", contacts, 20)
	if !strings.Contains(out, "Found 2 contact(s)") {
		t.Errorf("expected 2 grouped people: %s", out)
	}
	for _, want := range []string{"Alice Example", "Mobile: +1 555-111-2222", "Home: 555-333-4444", "Carol Roe"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q: %s", want, out)
		}
	}
}

// TestRenderContacts_LimitsPeople confirms the limit caps the number of distinct
// people shown, not raw rows.
func TestRenderContacts_LimitsPeople(t *testing.T) {
	contacts := []contactPhone{
		{name: "A", label: "Mobile", number: "111"},
		{name: "A", label: "Home", number: "222"}, // same person, second number
		{name: "B", label: "Mobile", number: "333"},
	}
	out := renderContacts("x", contacts, 1)
	if !strings.Contains(out, "Found 1 contact(s)") || strings.Contains(out, "B") {
		t.Errorf("limit=1 should show only the first person: %s", out)
	}
}

func TestRenderContacts_NoMatches(t *testing.T) {
	if out := renderContacts("zzz", nil, 20); !strings.Contains(out, "No contacts found") {
		t.Errorf("expected a no-matches message, got %q", out)
	}
}

func TestRunFindContact_RequiresName(t *testing.T) {
	if _, err := runFindContact(context.Background(), lookupCapability(t, "find_contact"), map[string]any{}); err == nil {
		t.Fatal("expected an error when 'name' is omitted")
	}
}
