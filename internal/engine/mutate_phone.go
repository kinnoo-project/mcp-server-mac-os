// mutate_phone.go implements the `call` mutator: it places a phone or FaceTime
// call by opening a `tel:` / `facetime:` / `facetime-audio:` URL.
//
// # Why this is irreversible (and why the URL is built in Go)
//
// Initiating a call is an outward-facing action with no undo, so — exactly like
// send_mail — `call` is staged with a verbatim preview and a nil Inverse; the
// human-approval gate is the existing `execute` step.
//
// The single most important guardrail is that the URL is ASSEMBLED HERE from a
// strictly-validated number, never passed through from the model. argv-splitting
// (the no-shell axiom) stops one class of injection, but it would NOT stop a
// model from supplying a value like "file:///etc/passwd" or "http://evil" that
// `open` would happily launch as a different scheme. canonicalizePhoneNumber
// rejects anything that is not digits + an optional leading "+" (after stripping
// formatting), so the value can only ever become the phone-number tail of a
// tel/facetime URL whose scheme this code chose.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// stageCall validates the request, resolves a number (from an explicit number or
// a Contacts name), builds the call URL, and stages an irreversible plan whose
// forward command is `open <url>`.
func stageCall(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	method, _ := getString(in, "method")
	number, _ := getString(in, "number")
	contactName, _ := getString(in, "contact_name")
	number = strings.TrimSpace(number)
	contactName = strings.TrimSpace(contactName)

	// Exactly one of number / contact_name. Requiring precisely one keeps the
	// target unambiguous: there is never a question of which source wins.
	if (number == "") == (contactName == "") {
		return nil, fmt.Errorf("call: provide exactly one of 'number' or 'contact_name'")
	}

	var resolvedName string // non-empty only when we looked the number up by name
	if contactName != "" {
		var err error
		number, resolvedName, err = resolveSingleNumber(ctx, contactName)
		if err != nil {
			return nil, err
		}
	}

	canonical, err := canonicalizePhoneNumber(number)
	if err != nil {
		return nil, err
	}
	url, err := callURL(method, canonical)
	if err != nil {
		return nil, err
	}

	target := canonical
	if resolvedName != "" {
		target = fmt.Sprintf("%s (%s)", resolvedName, canonical)
	}
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Place %s to %s.\n\nThis starts a real call and cannot be undone. Proceed?",
			methodLabel(method), target,
		),
		Forward: Command{Binary: "open", Args: []string{url}},
		Inverse: nil, // irreversible: a placed call has no undo
	}, nil
}

// resolveSingleNumber looks a name up in Contacts and returns the one number to
// call. It deliberately refuses ambiguity: if no one matches, or more than one
// DISTINCT number is found (whether across several people or one person with
// several numbers), it errors with the candidates so the caller can retry with
// an explicit `number`. Distinctness is judged on the canonicalized digits, so
// the same number stored under two labels (e.g. "mobile" and "iPhone") is not
// treated as ambiguous.
func resolveSingleNumber(ctx context.Context, name string) (number, displayName string, err error) {
	contacts, err := resolveContactNumbers(ctx, name)
	if err != nil {
		return "", "", err
	}
	if len(contacts) == 0 {
		return "", "", fmt.Errorf("call: no contact found matching %q", name)
	}

	// Collect distinct numbers (by canonical form). A number that fails
	// canonicalization is still offered as a candidate in the ambiguity message,
	// but cannot be auto-selected.
	seen := map[string]bool{}
	var distinct []contactPhone
	for _, c := range contacts {
		key, cerr := canonicalizePhoneNumber(c.number)
		if cerr != nil {
			key = c.number // keep it visible as a candidate, just not auto-selectable
		}
		if !seen[key] {
			seen[key] = true
			distinct = append(distinct, c)
		}
	}

	if len(distinct) == 1 {
		return distinct[0].number, distinct[0].name, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "call: %q is ambiguous — %d possible numbers. Re-issue the call with an explicit 'number':\n", name, len(distinct))
	for _, c := range distinct {
		label := c.label
		if label == "" {
			label = "phone"
		}
		fmt.Fprintf(&b, "  - %s (%s): %s\n", c.name, label, c.number)
	}
	return "", "", fmt.Errorf("%s", b.String())
}

// canonicalizePhoneNumber strips common formatting (spaces, dashes, parentheses,
// dots) and returns the bare number — an optional single leading "+" followed by
// digits. Anything else (letters, a ":" or "/" that could start another URL
// scheme, an interior "+") is rejected. This is the injection guard described in
// the file header: it bounds the value to something that can only be a phone
// number.
func canonicalizePhoneNumber(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("call: phone number is empty")
	}
	var b strings.Builder
	hasPlus := false
	for i, r := range trimmed {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '+' && i == 0:
			hasPlus = true
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
			// formatting: strip
		default:
			return "", fmt.Errorf("call: %q is not a valid phone number (only digits, an optional leading '+', and spaces/dashes/parentheses are allowed)", raw)
		}
	}
	canonical := b.String()
	digits := canonical
	if hasPlus {
		digits = canonical[1:]
	}
	if len(digits) < 3 || len(digits) > 15 {
		return "", fmt.Errorf("call: %q does not have a plausible number of digits (3–15)", raw)
	}
	return canonical, nil
}

// callURL builds the URL for a validated number and method. The scheme is chosen
// here, in code, so the model can never select it.
func callURL(method, number string) (string, error) {
	switch method {
	case "cellular":
		return "tel:" + number, nil
	case "facetime_audio":
		return "facetime-audio:" + number, nil
	case "facetime_video":
		return "facetime:" + number, nil
	default:
		// Unreachable when staged through the registry (the enum is validated),
		// but defended here because this function also runs in direct unit tests.
		return "", fmt.Errorf("call: unknown method %q", method)
	}
}

// methodLabel renders a method as the human phrase used in the staged preview.
func methodLabel(method string) string {
	switch method {
	case "cellular":
		return "a cellular phone call (via your iPhone)"
	case "facetime_audio":
		return "a FaceTime audio call"
	case "facetime_video":
		return "a FaceTime video call"
	default:
		return "a call"
	}
}
