// mutate_mail_test.go tests stageSendMail's validation, argv construction,
// and preview text.
//
// SAFETY: no test here ever calls RunCommand (or otherwise executes) the
// StagedPlan.Forward this produces. Doing so would send a REAL email via
// Mail.app with no way to undo it. Every test below only inspects the
// returned *StagedPlan's fields — Stage, by contract, never has side
// effects, which is exactly what these tests verify.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func TestStageSendMail_BuildsExpectedPlan(t *testing.T) {
	plan, err := stageSendMail(context.Background(), sendMailCapability(t), map[string]any{
		"to":      []string{"alice@example.com", "bob@example.com"},
		"subject": "Test subject",
		"body":    "Test body",
	})
	if err != nil {
		t.Fatalf("stageSendMail: %v", err)
	}

	if plan.Inverse != nil {
		t.Error("send_mail must be irreversible: Inverse should be nil")
	}
	if plan.Forward.Binary != "osascript" {
		t.Errorf("Forward.Binary = %q, want osascript", plan.Forward.Binary)
	}
	// Argv layout: ["-e", script, subject, body, recipients...].
	args := plan.Forward.Args
	if len(args) != 6 || args[0] != "-e" {
		t.Fatalf("unexpected argv shape: %v", args)
	}
	if args[2] != "Test subject" || args[3] != "Test body" {
		t.Errorf("subject/body not in expected positions: %v", args)
	}
	if args[4] != "alice@example.com" || args[5] != "bob@example.com" {
		t.Errorf("recipients not in expected positions: %v", args)
	}

	for _, want := range []string{"alice@example.com", "bob@example.com", "Test subject", "Test body", "CANNOT be undone"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %s", want, plan.Preview)
		}
	}
}

func TestStageSendMail_RejectsNoRecipients(t *testing.T) {
	_, err := stageSendMail(context.Background(), sendMailCapability(t), map[string]any{
		"to": []string{}, "subject": "x", "body": "y",
	})
	if err == nil {
		t.Fatal("expected an error for an empty recipient list")
	}
}

func TestStageSendMail_RejectsInvalidAddress(t *testing.T) {
	_, err := stageSendMail(context.Background(), sendMailCapability(t), map[string]any{
		"to": []string{"not-an-email"}, "subject": "x", "body": "y",
	})
	if err == nil {
		t.Fatal("expected an error for a recipient with no '@'")
	}
}

func TestStageSendMail_RejectsEmptySubject(t *testing.T) {
	_, err := stageSendMail(context.Background(), sendMailCapability(t), map[string]any{
		"to": []string{"alice@example.com"}, "subject": "", "body": "y",
	})
	if err == nil {
		t.Fatal("expected an error for an empty subject")
	}
}

func sendMailCapability(t *testing.T) registry.Capability {
	t.Helper()
	return lookupCapability(t, "send_mail")
}
