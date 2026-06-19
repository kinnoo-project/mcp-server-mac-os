// builtins_phone.go implements the read-only Contacts lookup, find_contact, and
// the shared contact-resolution helper the `call` mutator reuses to turn a name
// into a number at stage time.
//
// Like the Calendar/Reminders reads it drives Contacts.app through the hardened
// osascript path in applescript.go and parses tab-delimited stdout in Go. The
// call mutator itself lives in mutate_phone.go.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// find_contact caps how many people it displays: a readable default and a hard
// ceiling, mirroring the Calendar/Reminders read limits.
const (
	defaultFindContactLimit = 20
	maxFindContactLimit     = 50
)

// findContactScript emits one tab-delimited row per (person, phone) pair:
//
//	name \t label \t number
//
// argv (data, after "--"): the name query, then a max-people cap. The cap is
// enforced INSIDE the script (like query_events' maxN) so a broad query such as
// "a" can't return thousands of rows and flood the subprocess output before Go
// ever sees it. A person with several numbers still yields several rows, in
// Contacts' own order, so a caller can see every reachable number. Helper calls
// inside the `tell` block are `my`-prefixed so they resolve to this script's
// handlers, not Contacts' dictionary.
const findContactScript = asDateHelpers + `on run argv
	set theQuery to item 1 of argv
	set maxPeople to (item 2 of argv) as integer
	set out to ""
	set n to 0
	tell application "Contacts"
		repeat with p in (every person whose name contains theQuery)
			if n ≥ maxPeople then exit repeat
			repeat with ph in (phones of p)
				set out to out & my _clean(name of p) & tab & my _clean(label of ph) & tab & my _clean(value of ph) & linefeed
			end repeat
			set n to n + 1
		end repeat
	end tell
	return out
end run`

// contactPhone is one resolved (person, labeled number) pair.
type contactPhone struct {
	name   string // the contact's display name
	label  string // friendly phone label, e.g. "Mobile" / "Home" / "Work"
	number string // the phone number, as stored in Contacts (not yet canonicalized)
}

// runFindContact searches Contacts by name and renders the matches.
func runFindContact(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	name, _ := getString(in, "name")
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("find_contact: 'name' is required")
	}
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultFindContactLimit
	}
	if limit > maxFindContactLimit {
		limit = maxFindContactLimit
	}

	contacts, err := resolveContactNumbers(ctx, name, limit)
	if err != nil {
		return "", err
	}
	return renderContacts(name, contacts, limit), nil
}

// resolveContactNumbers runs the Contacts query (bounded to maxPeople matches)
// and parses every matching (person, number) pair. It is shared by find_contact
// (which renders the results) and the call mutator (which resolves a single
// number from them). A non-zero osascript exit — most commonly a not-yet-granted
// Contacts automation permission — is surfaced with an actionable hint.
func resolveContactNumbers(ctx context.Context, query string, maxPeople int) ([]contactPhone, error) {
	res, err := runOsascript(ctx, findContactScript, query, itoa(maxPeople))
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, phoneScriptError("find_contact", res.Stderr)
	}
	return parseContactRows(res.Stdout), nil
}

// parseContactRows turns the script's "name\tlabel\tnumber" rows into
// contactPhone values, applying friendlyLabel and skipping any malformed row
// (so one odd record can't break the whole lookup).
func parseContactRows(stdout string) []contactPhone {
	var out []contactPhone
	for _, row := range asRows(stdout) {
		f := strings.Split(row, "\t")
		if len(f) != 3 {
			continue
		}
		number := strings.TrimSpace(f[2])
		if number == "" {
			continue
		}
		out = append(out, contactPhone{name: strings.TrimSpace(f[0]), label: friendlyLabel(f[1]), number: number})
	}
	return out
}

// renderContacts groups the (person, number) rows by person, caps the number of
// people shown to limit, and formats them for the model/user to read.
func renderContacts(query string, contacts []contactPhone, limit int) string {
	if len(contacts) == 0 {
		return fmt.Sprintf("No contacts found matching %q.", query)
	}

	// Group consecutive rows by name (the script emits a person's numbers
	// together), preserving Contacts' order, and stop after `limit` people.
	type person struct {
		name   string
		phones []contactPhone
	}
	var people []person
	for _, c := range contacts {
		if n := len(people); n > 0 && people[n-1].name == c.name {
			people[n-1].phones = append(people[n-1].phones, c)
			continue
		}
		if len(people) == limit {
			break
		}
		people = append(people, person{name: c.name, phones: []contactPhone{c}})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d contact(s) matching %q:\n", len(people), query)
	for _, p := range people {
		fmt.Fprintf(&b, "  %s\n", p.name)
		for _, ph := range p.phones {
			fmt.Fprintf(&b, "    %s: %s\n", labelOrPhone(ph.label), ph.number)
		}
	}
	return b.String()
}

// labelOrPhone returns a phone label, defaulting an empty one to "phone" so a
// number with no label still reads sensibly in output and candidate lists.
func labelOrPhone(label string) string {
	if label == "" {
		return "phone"
	}
	return label
}

// friendlyLabel strips Apple's internal label wrapper (`_$!<Mobile>!$_`) down to
// a plain word ("Mobile"), and passes a custom or already-plain label through
// unchanged.
func friendlyLabel(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "_$!<")
	s = strings.TrimSuffix(s, ">!$_")
	return s
}

// phoneScriptError mirrors calendarScriptError / remindersScriptError: it hints
// at the most likely cause (Contacts automation access not granted) rather than
// surfacing a bare AppleScript error.
func phoneScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: Contacts automation failed (%s). If this is the first use, macOS may need you to grant this app access to Contacts in System Settings → Privacy & Security → Automation", op, stderr)
}
