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
// call. The lookup is bounded (a broad query can't flood Contacts output);
// chooseContactNumber then applies the selection policy.
func resolveSingleNumber(ctx context.Context, name string) (number, displayName string, err error) {
	contacts, err := resolveContactNumbers(ctx, name, maxFindContactLimit)
	if err != nil {
		return "", "", err
	}
	return chooseContactNumber(name, contacts)
}

// chooseContactNumber applies the resolution policy to a set of matched
// contacts. It auto-selects EXACTLY when there is a single distinct, dialable
// number; otherwise it returns an actionable error (no match; the only number
// isn't dialable; or genuinely ambiguous). Distinctness is judged on the
// canonicalized digits, so the same number under two labels ("mobile"/"iPhone")
// counts once. It is split out from the live Contacts query so the decision
// logic can be unit-tested.
func chooseContactNumber(name string, contacts []contactPhone) (number, displayName string, err error) {
	if len(contacts) == 0 {
		return "", "", fmt.Errorf("call: no contact found matching %q", name)
	}

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

	// Auto-select only a single candidate that actually canonicalizes. A lone
	// but un-dialable number falls through to a clear error rather than being
	// returned here and then failing canonicalization in the caller — which is
	// what the previous code did, contradicting its own "cannot be
	// auto-selected" intent.
	if len(distinct) == 1 {
		only := distinct[0]
		if _, cerr := canonicalizePhoneNumber(only.number); cerr == nil {
			return only.number, only.name, nil
		}
		return "", "", fmt.Errorf("call: the only number found for %q (%s: %s) is not in a dialable format; re-issue with an explicit 'number'", name, labelOrPhone(only.label), only.number)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "call: %q is ambiguous — %d possible numbers. Re-issue the call with an explicit 'number':\n", name, len(distinct))
	for _, c := range distinct {
		fmt.Fprintf(&b, "  - %s (%s): %s\n", c.name, labelOrPhone(c.label), c.number)
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
