// builtins_contacts.go implements get_contact — the read-only "show me this
// person's full card" lookup over Contacts.app. It complements
// application-phone/find_contact (builtins_phone.go), which returns numbers only:
// get_contact returns every field on the card (phones, emails, postal addresses,
// birthday, organization).
//
// Like the other application reads it drives Contacts through the hardened
// osascript seam in applescript.go — a FIXED script whose model-supplied value
// (the name query) reaches it strictly as `on run argv` data after the "--"
// terminator — and parses tab-delimited stdout in Go. The create_contact mutator
// lives in mutate_contacts.go.
//
// # Guardrails
//
//   - The name query is free text but crosses into AppleScript only as data after
//     the "--" terminator runOsascript inserts, so a value like "-e" is inert
//     (proven by TestGetContact_HostileNameLandsAsData); get_contact therefore
//     carries a reviewedFreeTextBuiltins entry.
//   - Every emitted field is _clean-flattened so a value containing a tab or
//     newline cannot corrupt the one-row-per-line output contract the Go parser
//     relies on, and a `missing value` property coerces to empty via _str rather
//     than erroring.
//   - The number of people returned is capped inside the script (like
//     find_contact's maxPeople) so a broad query cannot flood the subprocess
//     output, and the rendered card is bounded by the shared compactOutput budget.
//
// # Privacy
//
// A contact card is squarely personal data. The manifest description flags that
// so a caller reaches for it deliberately.
package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// get_contact caps how many people it displays: a small readable default and a
// hard ceiling, mirroring find_contact's limits (a full card is much larger than
// a single number row, so the default is smaller).
const (
	defaultGetContactLimit = 5
	maxGetContactLimit     = 20
)

// getContactAddrHelper is a small AppleScript handler, prepended to the script,
// that renders a Contacts address record as a single comma-joined line, skipping
// empty components so a partially-filled address does not read as ", , ,". It is
// kept out of asDateHelpers because only this capability needs it.
const getContactAddrHelper = `on _addr(ad)
	set parts to {}
	set s to my _str(street of ad)
	if s is not "" then set end of parts to s
	set c to my _str(city of ad)
	if c is not "" then set end of parts to c
	set st to my _str(state of ad)
	if st is not "" then set end of parts to st
	set z to my _str(zip of ad)
	if z is not "" then set end of parts to z
	set cn to my _str(country of ad)
	if cn is not "" then set end of parts to cn
	set savedTID to AppleScript's text item delimiters
	set AppleScript's text item delimiters to ", "
	set r to parts as string
	set AppleScript's text item delimiters to savedTID
	return r
end _addr

`

// getContactScript emits one tab-delimited row per field of each matching person:
//
//	personIndex \t fieldType \t label \t value
//
// fieldType is one of name/organization/birthday/phone/email/address. Emitting a
// typed row per field (rather than one wide row per person) keeps the output a
// simple one-record-per-line stream even though a person has a variable number of
// phones, emails, and addresses — the Go side groups the rows by personIndex to
// rebuild each card.
//
// argv (data, after "--"): the name query, then a max-people cap. The cap is
// enforced INSIDE the script (like find_contact's maxPeople) so a broad query
// cannot return thousands of people before Go ever sees the output. Helper calls
// inside the `tell` block are `my`-prefixed so they resolve to this script's
// _clean/_str/_addr/_fmt handlers rather than Contacts' dictionary.
const getContactScript = asDateHelpers + getContactAddrHelper + `on run argv
	set theQuery to item 1 of argv
	set maxPeople to (item 2 of argv) as integer
	set out to ""
	set n to 0
	tell application "Contacts"
		launch
		repeat with p in (every person whose name contains theQuery)
			if n ≥ maxPeople then exit repeat
			set idx to (n + 1) as string
			set out to out & idx & tab & "name" & tab & "" & tab & my _clean(name of p) & linefeed
			set org to my _str(organization of p)
			if org is not "" then set out to out & idx & tab & "organization" & tab & "" & tab & my _clean(org) & linefeed
			set bd to birth date of p
			if bd is not missing value then set out to out & idx & tab & "birthday" & tab & "" & tab & my _fmt(bd) & linefeed
			repeat with ph in (phones of p)
				set out to out & idx & tab & "phone" & tab & my _clean(label of ph) & tab & my _clean(value of ph) & linefeed
			end repeat
			repeat with em in (emails of p)
				set out to out & idx & tab & "email" & tab & my _clean(label of em) & tab & my _clean(value of em) & linefeed
			end repeat
			repeat with ad in (addresses of p)
				set out to out & idx & tab & "address" & tab & my _clean(label of ad) & tab & my _clean(my _addr(ad)) & linefeed
			end repeat
			set n to n + 1
		end repeat
	end tell
	return out
end run`

// contactField is one parsed field row from getContactScript.
type contactField struct {
	person int    // 1-based person index, grouping fields of the same card
	kind   string // name/organization/birthday/phone/email/address
	label  string // friendly label for phone/email/address; empty otherwise
	value  string // the field value (already _clean-flattened by the script)
}

// runGetContact searches Contacts by name and renders each match's full card.
func runGetContact(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	name, _ := getString(in, "name")
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("get_contact: 'name' is required")
	}
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultGetContactLimit
	}
	if limit > maxGetContactLimit {
		limit = maxGetContactLimit
	}

	res, err := runOsascript(ctx, getContactScript, name, itoa(limit))
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		// phoneScriptError already names Contacts + the Automation grant path; the
		// permission surface is identical, so reuse it rather than duplicate.
		return "", phoneScriptError("get_contact", res.Stderr)
	}

	fields := parseContactFields(res.Stdout)
	if len(fields) == 0 {
		return fmt.Sprintf("No contacts found matching %q.", name), nil
	}
	return compactOutput(renderContactCards(name, fields)), nil
}

// parseContactFields turns the script's "index\tkind\tlabel\tvalue" rows into
// contactField values, skipping any malformed row (so one odd record cannot break
// the whole lookup, matching parseContactRows/parseTabRows).
func parseContactFields(stdout string) []contactField {
	var out []contactField
	for _, row := range asRows(stdout) {
		f := strings.Split(row, "\t")
		if len(f) != 4 {
			continue
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		out = append(out, contactField{person: idx, kind: f[1], label: friendlyLabel(f[2]), value: f[3]})
	}
	return out
}

// renderContactCards groups the typed field rows by person (they arrive grouped,
// in Contacts' own order) and formats each as a readable card.
func renderContactCards(query string, fields []contactField) string {
	type card struct {
		index  int
		fields []contactField
	}
	var cards []card
	for _, f := range fields {
		if n := len(cards); n > 0 && cards[n-1].index == f.person {
			cards[n-1].fields = append(cards[n-1].fields, f)
			continue
		}
		cards = append(cards, card{index: f.person, fields: []contactField{f}})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d contact(s) matching %q:\n", len(cards), query)
	for _, c := range cards {
		// The "name" field leads each card; fall back to a placeholder if absent.
		b.WriteString("  " + orUntitled(cardName(c.fields)) + "\n")
		for _, f := range c.fields {
			switch f.kind {
			case "name":
				// Already used as the card heading.
			case "organization":
				fmt.Fprintf(&b, "    Organization: %s\n", f.value)
			case "birthday":
				fmt.Fprintf(&b, "    Birthday: %s\n", contactBirthday(f.value))
			case "phone", "email", "address":
				fmt.Fprintf(&b, "    %s (%s): %s\n", titleKind(f.kind), labelOr(f.label, f.kind), f.value)
			}
		}
	}
	return b.String()
}

// cardName returns the person's display name from its field rows, or "" if the
// name row was somehow missing.
func cardName(fields []contactField) string {
	for _, f := range fields {
		if f.kind == "name" {
			return f.value
		}
	}
	return ""
}

// contactBirthday trims the "YYYY-MM-DD HH:MM" that _fmt emits down to the date,
// since a birthday carries no meaningful time of day.
func contactBirthday(v string) string {
	if date, _, ok := strings.Cut(v, " "); ok {
		return date
	}
	return v
}

// titleKind capitalizes a field kind for display ("phone" -> "Phone").
func titleKind(kind string) string {
	if kind == "" {
		return kind
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

// labelOr returns the field's friendly label, defaulting an empty one to the
// field kind so a value with no label still reads sensibly.
func labelOr(label, kind string) string {
	if strings.TrimSpace(label) == "" {
		return kind
	}
	return label
}
