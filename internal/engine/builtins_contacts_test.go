// builtins_contacts_test.go covers the pure Go halves of get_contact — parsing
// the typed field rows the AppleScript emits and rendering them into readable
// cards — plus the structural injection guarantee for its free-text name query.
// None of these tests drive osascript or touch the real Contacts database; they
// exercise the parse/render/argv logic directly with synthetic input.
package engine

import (
	"strings"
	"testing"
)

// TestParseContactFields_GroupsAndSkipsMalformed verifies that well-formed
// "index\tkind\tlabel\tvalue" rows parse into contactField values (with the phone
// label de-wrapped via friendlyLabel) and that a row with the wrong field count
// or a non-numeric index is skipped rather than corrupting the whole listing.
func TestParseContactFields_GroupsAndSkipsMalformed(t *testing.T) {
	stdout := strings.Join([]string{
		"1\tname\t\tJane Doe",
		"1\tphone\t_$!<Mobile>!$_\t+15555550123",
		"1\temail\t_$!<Work>!$_\tjane@example.com",
		"bad\tname\t\tShould Skip",   // non-numeric index -> skipped
		"2\tname\t\tJohn Roe\textra", // 5 fields -> skipped
		"2\tname\t\tJohn Roe",
	}, "\n")

	fields := parseContactFields(stdout)
	if len(fields) != 4 {
		t.Fatalf("expected 4 parsed fields (2 malformed skipped), got %d: %+v", len(fields), fields)
	}
	if fields[1].kind != "phone" || fields[1].label != "Mobile" || fields[1].value != "+15555550123" {
		t.Errorf("phone row parsed/labeled wrong: %+v", fields[1])
	}
	if fields[3].person != 2 || fields[3].value != "John Roe" {
		t.Errorf("second person's name row parsed wrong: %+v", fields[3])
	}
}

// TestRenderContactCards_GroupsByPerson checks that fields are grouped per person
// under the name heading, that a birthday is trimmed to just the date, and that a
// labelless field falls back to its kind for the label.
func TestRenderContactCards_GroupsByPerson(t *testing.T) {
	fields := []contactField{
		{person: 1, kind: "name", value: "Jane Doe"},
		{person: 1, kind: "organization", value: "Example Corp"},
		{person: 1, kind: "birthday", value: "1990-04-01 00:00"},
		{person: 1, kind: "phone", label: "Mobile", value: "+15555550123"},
		{person: 1, kind: "email", label: "", value: "jane@example.com"},
		{person: 2, kind: "name", value: "John Roe"},
	}

	out := renderContactCards("doe", fields)
	if !strings.Contains(out, "Found 2 contact(s) matching \"doe\":") {
		t.Errorf("missing/incorrect heading:\n%s", out)
	}
	if !strings.Contains(out, "Jane Doe") || !strings.Contains(out, "John Roe") {
		t.Errorf("both names should appear:\n%s", out)
	}
	if !strings.Contains(out, "Organization: Example Corp") {
		t.Errorf("organization should render:\n%s", out)
	}
	if !strings.Contains(out, "Birthday: 1990-04-01") || strings.Contains(out, "00:00") {
		t.Errorf("birthday should be trimmed to the date:\n%s", out)
	}
	if !strings.Contains(out, "Phone (Mobile): +15555550123") {
		t.Errorf("labeled phone should render:\n%s", out)
	}
	if !strings.Contains(out, "Email (email): jane@example.com") {
		t.Errorf("labelless email should fall back to its kind:\n%s", out)
	}
}

// TestGetContact_HostileNameLandsAsData is the injection regression test the
// reviewedFreeTextBuiltins entry points at: get_contact's name query — even a
// value like "-e" that osascript would otherwise read as its own flag — reaches
// the fixed script strictly as data AFTER the "--" terminator, never as code.
func TestGetContact_HostileNameLandsAsData(t *testing.T) {
	for _, h := range hostileValues {
		argv := osascriptArgv(getContactScript, h, "5")
		// argv must be exactly: -e <script> -- <name> <limit>
		if len(argv) != 5 {
			t.Fatalf("%q: unexpected argv length: %q", h, argv)
		}
		if argv[0] != "-e" || argv[1] != getContactScript || argv[2] != "--" {
			t.Errorf("%q: expected leading -e <script> -- ..., got %q", h, argv[:3])
		}
		if argv[3] != h {
			t.Errorf("%q: name landed as %q, want the hostile value verbatim after '--'", h, argv[3])
		}
	}
}
