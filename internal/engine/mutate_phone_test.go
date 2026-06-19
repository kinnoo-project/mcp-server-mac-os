// mutate_phone_test.go tests the call mutator's number validation, URL
// construction, and staging — including the option/scheme-injection guard.
//
// SAFETY: no test executes a StagedPlan, so no real call is ever placed. The
// contact_name path runs a live Contacts probe, so only its pre-resolution
// validation (the exactly-one-of guard) is unit-tested; calls with an explicit
// number are tested in full.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func callCapability(t *testing.T) registry.Capability { return lookupCapability(t, "call") }

func TestCanonicalizePhoneNumber(t *testing.T) {
	ok := map[string]string{
		"+1 (555) 123-4567": "+15551234567",
		"555-1234":          "5551234",
		"  555.123.4567 ":   "5551234567",
		"911":               "911",
	}
	for in, want := range ok {
		got, err := canonicalizePhoneNumber(in)
		if err != nil || got != want {
			t.Errorf("canonicalizePhoneNumber(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Rejected: empty, scheme-like / injection attempts, letters, interior '+',
	// too short, too long.
	for _, bad := range []string{"", "tel:911", "facetime:evil", "http://x", "+1/etc", "abcdef", "12", "+", "1+2", strings.Repeat("9", 16)} {
		if got, err := canonicalizePhoneNumber(bad); err == nil {
			t.Errorf("canonicalizePhoneNumber(%q) should have failed, got %q", bad, got)
		}
	}
}

func TestStageCall_ExplicitNumberCellular(t *testing.T) {
	plan, err := stageCall(context.Background(), callCapability(t), map[string]any{
		"method": "cellular", "number": "+1 (555) 123-4567",
	})
	if err != nil {
		t.Fatalf("stageCall: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("call must be irreversible: Inverse should be nil")
	}
	if plan.Forward.Binary != "open" {
		t.Errorf("Forward.Binary = %q, want open", plan.Forward.Binary)
	}
	if len(plan.Forward.Args) != 1 || plan.Forward.Args[0] != "tel:+15551234567" {
		t.Errorf("Forward.Args = %v, want [tel:+15551234567]", plan.Forward.Args)
	}
	for _, want := range []string{"+15551234567", "cellular", "cannot be undone"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %s", want, plan.Preview)
		}
	}
}

func TestStageCall_MethodURLs(t *testing.T) {
	cases := map[string]string{
		"cellular":       "tel:5551234567",
		"facetime_audio": "facetime-audio:5551234567",
		"facetime_video": "facetime:5551234567",
	}
	for method, wantURL := range cases {
		plan, err := stageCall(context.Background(), callCapability(t), map[string]any{
			"method": method, "number": "555-123-4567",
		})
		if err != nil {
			t.Fatalf("stageCall(%s): %v", method, err)
		}
		if plan.Forward.Args[0] != wantURL {
			t.Errorf("method %s built URL %q, want %q", method, plan.Forward.Args[0], wantURL)
		}
	}
}

// TestStageCall_RejectsBadNumberBecomesNoOtherScheme is the scheme-injection
// regression: a number that tries to smuggle in another URL scheme is rejected
// before any URL is built, so `open` can never be handed something other than a
// tel/facetime URL this code constructed.
func TestStageCall_RejectsBadNumber(t *testing.T) {
	for _, bad := range []string{"file:///etc/passwd", "http://evil.example", "tel:1;rm", "evil"} {
		if _, err := stageCall(context.Background(), callCapability(t), map[string]any{
			"method": "cellular", "number": bad,
		}); err == nil {
			t.Errorf("stageCall with number %q should have been rejected", bad)
		}
	}
}

// TestChooseContactNumber covers the contact-resolution policy that the
// contact_name path depends on (split out from the live Contacts query so it can
// be tested without a subprocess).
func TestChooseContactNumber(t *testing.T) {
	// No matches.
	if _, _, err := chooseContactNumber("Nobody", nil); err == nil {
		t.Error("expected an error when no contact matches")
	}

	// Single dialable number → auto-selected.
	num, name, err := chooseContactNumber("Alice", []contactPhone{
		{name: "Alice Example", label: "Mobile", number: "+1 (555) 123-4567"},
	})
	if err != nil || num != "+1 (555) 123-4567" || name != "Alice Example" {
		t.Errorf("single dialable number = %q, %q, %v", num, name, err)
	}

	// Same number under two labels → still one distinct → auto-selected.
	if _, _, err := chooseContactNumber("Alice", []contactPhone{
		{name: "Alice", label: "Mobile", number: "555-1234"},
		{name: "Alice", label: "iPhone", number: "(555) 1234"},
	}); err != nil {
		t.Errorf("one distinct number across labels should auto-select, got %v", err)
	}

	// Single but UN-dialable number → not auto-selected; actionable error. This
	// is the regression for Copilot's comment: the old code returned it and let
	// canonicalization fail later with a less helpful message.
	if _, _, err := chooseContactNumber("Weird", []contactPhone{
		{name: "Weird Contact", label: "Home", number: "ext. 4321"},
	}); err == nil || !strings.Contains(err.Error(), "not in a dialable format") {
		t.Errorf("a lone un-dialable number should error clearly, got %v", err)
	}

	// Two distinct numbers → ambiguous, both listed.
	_, _, err = chooseContactNumber("Bob", []contactPhone{
		{name: "Bob", label: "Mobile", number: "555-1111"},
		{name: "Bob", label: "Work", number: "555-2222"},
	})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("two distinct numbers should be ambiguous, got %v", err)
	}
}

func TestStageCall_RequiresExactlyOneTarget(t *testing.T) {
	// Neither number nor contact_name.
	if _, err := stageCall(context.Background(), callCapability(t), map[string]any{"method": "cellular"}); err == nil {
		t.Error("expected an error when neither number nor contact_name is given")
	}
	// Both — must reject before any Contacts probe.
	if _, err := stageCall(context.Background(), callCapability(t), map[string]any{
		"method": "cellular", "number": "5551234", "contact_name": "Alice",
	}); err == nil {
		t.Error("expected an error when both number and contact_name are given")
	}
}
