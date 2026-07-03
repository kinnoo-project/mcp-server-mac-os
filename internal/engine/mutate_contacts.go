// mutate_contacts.go implements create_contact — adding a new person to
// Contacts.app — as a reversible staged plan, the mutating counterpart to the
// read-only get_contact in builtins_contacts.go.
//
// # The stage-time id problem, solved with a marker
//
// A mutator resolves BOTH its forward and inverse commands at stage time (see
// mutate.go), but a brand-new contact has no stable identifier until it is
// actually created at commit time — so the inverse cannot name the person by id.
// create_contact solves this the way the plan prescribes: the forward writes a
// unique, randomly-generated marker string into the new person's `note` field,
// and the inverse deletes the person whose note EQUALS that marker. Because the
// marker is cryptographically unique, the compensating delete can only ever match
// the one contact this operation created — no pre-existing card can collide with
// it (contrast create_note, whose title-based inverse can match a same-title
// note). This is why the operation is `compensatable` (the inverse is a targeted
// delete, not a byte-exact restore of prior state).
//
// # Guardrails
//
// All model-supplied field values cross into AppleScript as "--"-terminated
// `on run argv` data (osascriptCommand), so a value beginning with "-" is inert
// — proven by TestStageCreateContact_HostileFieldLandsAsData. On top of that
// structural guard the fields are validated for shape: phone must look like a
// phone number, email like an email, and no field may carry a control character.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// createContactScript makes one new person and (optionally) attaches a phone and
// an email, then tags the card with a unique marker in its note field so the
// inverse can find exactly this contact. argv (data, after "--"):
//
//	firstName, lastName, organization, phone, email, marker
//
// Empty identity/contact fields are left unset rather than written as blanks, so
// a card created with only a first name does not carry an empty organization.
const createContactScript = `on run argv
	set fn to item 1 of argv
	set ln to item 2 of argv
	set org to item 3 of argv
	set ph to item 4 of argv
	set em to item 5 of argv
	set marker to item 6 of argv
	tell application "Contacts"
		set p to make new person with properties {note:marker}
		if fn is not "" then set first name of p to fn
		if ln is not "" then set last name of p to ln
		if org is not "" then set organization of p to org
		if ph is not "" then make new phone at end of phones of p with properties {label:"main", value:ph}
		if em is not "" then make new email at end of emails of p with properties {label:"work", value:em}
		save
	end tell
end run`

// deleteContactByMarkerScript removes every person whose note equals the marker.
// argv (data, after "--"): marker. Because the marker is unique to a single
// create_contact, this can only ever delete the contact that operation created.
const deleteContactByMarkerScript = `on run argv
	set marker to item 1 of argv
	tell application "Contacts"
		set matches to (every person whose note is marker)
		repeat with p in matches
			delete p
		end repeat
		save
	end tell
end run`

// stageCreateContact validates the requested fields, mints a unique deletion
// marker, and returns a plan whose inverse removes exactly the created contact.
func stageCreateContact(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	first, _ := getString(in, "first_name")
	last, _ := getString(in, "last_name")
	org, _ := getString(in, "organization")
	phone, _ := getString(in, "phone")
	email, _ := getString(in, "email")

	first = strings.TrimSpace(first)
	last = strings.TrimSpace(last)
	org = strings.TrimSpace(org)
	phone = strings.TrimSpace(phone)
	email = strings.TrimSpace(email)

	// A person needs SOME identity; refuse a card that would be entirely blank.
	if first == "" && last == "" && org == "" {
		return nil, fmt.Errorf("create_contact: provide at least one of 'first_name', 'last_name', or 'organization'")
	}
	// Reject control characters in EVERY free-text field (the "--" terminator
	// already defeats option injection; this guards data quality and the one-line
	// preview, and closes the gap that plausibleEmail's regex would let a NUL or
	// other control byte through in an email). Ordered, not a map, so the field
	// named in the error is deterministic.
	for _, f := range []struct{ name, val string }{
		{"first_name", first}, {"last_name", last}, {"organization", org},
		{"phone", phone}, {"email", email},
	} {
		if err := rejectControlChars("create_contact", f.name, f.val); err != nil {
			return nil, err
		}
	}
	if phone != "" {
		if err := validateContactPhone(phone); err != nil {
			return nil, err
		}
	}
	if email != "" && !plausibleEmail(email) {
		return nil, fmt.Errorf("create_contact: %q does not look like a valid email address", email)
	}

	marker, err := newContactMarker()
	if err != nil {
		return nil, err
	}

	inverse := osascriptCommand(deleteContactByMarkerScript, marker)
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Create a new contact: %s.\n\nUndo will delete this exact contact (matched by a hidden unique marker, so no other card can be affected).",
			describeContact(first, last, org, phone, email),
		),
		Forward: osascriptCommand(createContactScript, first, last, org, phone, email, marker),
		Inverse: &inverse,
	}, nil
}

// validateContactPhone accepts a phone number in human form (digits, an optional
// leading '+', and spaces/dashes/parentheses/dots) and rejects anything with
// letters, a control character, or an implausible digit count. Unlike the `call`
// mutator it does NOT strip formatting — a contact card should keep the number as
// the user wrote it — so it validates in place and preserves the original string.
// It still enforces the same 3–15 digit range as canonicalizePhoneNumber so a
// value made only of separators (e.g. "()" or "--") that carries no actual digits
// cannot be staged as a "phone number".
func validateContactPhone(raw string) error {
	digits := 0
	for i, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '+' && i == 0:
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
		default:
			return fmt.Errorf("create_contact: %q is not a valid phone number (only digits, an optional leading '+', and spaces/dashes/dots/parentheses are allowed)", raw)
		}
	}
	if digits < 3 || digits > 15 {
		return fmt.Errorf("create_contact: %q does not have a plausible number of digits (3–15)", raw)
	}
	return nil
}

// rejectControlChars refuses a field value containing an ASCII control character
// (including NUL and newline), which would corrupt the one-line staging preview
// and has no place in a name or organization.
func rejectControlChars(op, field, val string) error {
	for _, r := range val {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s: '%s' must not contain control characters", op, field)
		}
	}
	return nil
}

// newContactMarker returns a unique, human-recognizable marker string to tag a
// created contact's note field so its inverse can find exactly that card. The
// random suffix comes from crypto/rand, matching the token generation used
// elsewhere (internal/transaction, messages_sandbox).
func newContactMarker() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("create_contact: could not generate a deletion marker: %w", err)
	}
	return "mcp-created-contact-" + hex.EncodeToString(buf), nil
}

// describeContact renders a compact, human-readable one-line summary of the card
// being created for the staging preview.
func describeContact(first, last, org, phone, email string) string {
	var parts []string
	if name := strings.TrimSpace(first + " " + last); name != "" {
		parts = append(parts, name)
	}
	if org != "" {
		parts = append(parts, "at "+org)
	}
	if phone != "" {
		parts = append(parts, "phone "+phone)
	}
	if email != "" {
		parts = append(parts, "email "+email)
	}
	return strings.Join(parts, ", ")
}
