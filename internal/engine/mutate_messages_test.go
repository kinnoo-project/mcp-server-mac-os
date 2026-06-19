// mutate_messages_test.go tests send_message's validation, argv construction,
// and preview.
//
// SAFETY: no test executes a StagedPlan, so no real iMessage is ever sent. The
// contact_name path runs a live Contacts probe, so only the explicit-handle path
// and pre-resolution validation are unit-tested.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func sendMessageCapability(t *testing.T) registry.Capability {
	return lookupCapability(t, "send_message")
}

func TestStageSendMessage_ExplicitPhoneHandle(t *testing.T) {
	plan, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "+1 (555) 123-4567", "text": "on my way",
	})
	if err != nil {
		t.Fatalf("stageSendMessage: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("send_message must be irreversible: Inverse should be nil")
	}
	if plan.Forward.Binary != "osascript" {
		t.Errorf("Forward.Binary = %q, want osascript", plan.Forward.Binary)
	}
	a := plan.Forward.Args
	// ["-e", sendIMessageScript, "--", handle, text]
	if len(a) != 5 || a[1] != sendIMessageScript {
		t.Fatalf("unexpected argv: %v", a)
	}
	if a[2] != "--" {
		t.Errorf("missing osascript terminator at index 2: %v", a)
	}
	if a[3] != "+15551234567" || a[4] != "on my way" {
		t.Errorf("handle/text not in expected positions (handle should be canonicalized): %v", a[3:])
	}
	for _, want := range []string{"+15551234567", "on my way", "cannot be undone"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %s", want, plan.Preview)
		}
	}
}

func TestStageSendMessage_EmailHandle(t *testing.T) {
	plan, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "alice@example.com", "text": "hi",
	})
	if err != nil {
		t.Fatalf("stageSendMessage: %v", err)
	}
	if plan.Forward.Args[3] != "alice@example.com" {
		t.Errorf("email handle should pass through unchanged: %v", plan.Forward.Args)
	}
}

// TestStageSendMessage_FlagLikeTextStaysData is the option-injection regression:
// a message body of "-e" must land as data after the "--" terminator.
func TestStageSendMessage_FlagLikeTextStaysData(t *testing.T) {
	plan, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "5551234567", "text": "-e",
	})
	if err != nil {
		t.Fatalf("stageSendMessage: %v", err)
	}
	a := plan.Forward.Args
	if a[2] != "--" || a[4] != "-e" {
		t.Fatalf("flag-like text not neutralized by terminator: %v", a)
	}
}

func TestStageSendMessage_Rejects(t *testing.T) {
	cases := map[string]map[string]any{
		"missing text":     {"handle": "5551234567"},
		"no recipient":     {"text": "hi"},
		"both recipients":  {"handle": "5551234567", "contact_name": "Alice", "text": "hi"},
		"bad phone handle": {"handle": "not-a-number", "text": "hi"},
		"bad email handle": {"handle": "broken@", "text": "hi"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := stageSendMessage(context.Background(), sendMessageCapability(t), params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

func TestValidateSendHandle(t *testing.T) {
	if got, err := validateSendHandle("+1 555-123-4567", false); err != nil || got != "+15551234567" {
		t.Errorf("phone handle = %q, %v", got, err)
	}
	if got, err := validateSendHandle("a@b.com", true); err != nil || got != "a@b.com" {
		t.Errorf("email handle = %q, %v", got, err)
	}
	if _, err := validateSendHandle("broken@", true); err == nil {
		t.Error("invalid email should be rejected")
	}
}
