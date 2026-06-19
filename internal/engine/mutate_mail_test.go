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
	"os"
	"path/filepath"
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
	// Argv layout: ["-e", script, "--", subject, body, recipientCount, recipients...].
	// The "--" at index 2 is the osascript end-of-options terminator that stops
	// a model-supplied value like subject="-e" from being parsed as a flag.
	args := plan.Forward.Args
	if len(args) != 8 || args[0] != "-e" {
		t.Fatalf("unexpected argv shape: %v", args)
	}
	if args[2] != "--" {
		t.Errorf("expected \"--\" end-of-options terminator at index 2: %v", args)
	}
	if args[3] != "Test subject" || args[4] != "Test body" {
		t.Errorf("subject/body not in expected positions: %v", args)
	}
	if args[5] != "2" {
		t.Errorf("recipient count = %q, want \"2\": %v", args[5], args)
	}
	if args[6] != "alice@example.com" || args[7] != "bob@example.com" {
		t.Errorf("recipients not in expected positions: %v", args)
	}

	for _, want := range []string{"alice@example.com", "bob@example.com", "Test subject", "Test body", "cannot be undone", "Send this email?"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %s", want, plan.Preview)
		}
	}
	if strings.Contains(plan.Preview, "Attachments:") {
		t.Errorf("preview should not mention attachments when there are none: %s", plan.Preview)
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

// TestStageSendMail_WithAttachment confirms a real, existing file is
// accepted: it's appended to argv after the recipients, and its filename
// (not the full path) appears in the preview.
func TestStageSendMail_WithAttachment(t *testing.T) {
	dir := t.TempDir()
	attachment := filepath.Join(dir, "itinerary.pdf")
	if err := os.WriteFile(attachment, []byte("fake pdf bytes"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	plan, err := stageSendMail(context.Background(), sendMailCapability(t), map[string]any{
		"to": []string{"alice@example.com"}, "subject": "x", "body": "y",
		"attachments": []string{attachment},
	})
	if err != nil {
		t.Fatalf("stageSendMail: %v", err)
	}

	args := plan.Forward.Args
	// ["-e", script, "--", subject, body, "1", "alice@example.com", attachment]
	if len(args) != 8 || args[7] != attachment {
		t.Fatalf("expected the attachment path appended after recipients, got: %v", args)
	}
	if !strings.Contains(plan.Preview, "Attachments: itinerary.pdf") {
		t.Errorf("preview should name the attachment by filename: %s", plan.Preview)
	}
}

// TestStageSendMail_RejectsMissingAttachment confirms staging probes that
// each attachment actually exists, the same read-before-stage discipline
// mkdir uses for its target path — refusing here is much better than
// discovering the problem only when osascript fails mid-send.
func TestStageSendMail_RejectsMissingAttachment(t *testing.T) {
	_, err := stageSendMail(context.Background(), sendMailCapability(t), map[string]any{
		"to": []string{"alice@example.com"}, "subject": "x", "body": "y",
		"attachments": []string{"/nonexistent/path/does-not-exist.pdf"},
	})
	if err == nil {
		t.Fatal("expected an error for a nonexistent attachment path")
	}
}

// TestStageSendMail_RejectsDirectoryAttachment confirms a directory is
// rejected rather than passed through to an AppleScript "make new
// attachment" call that doesn't support folders.
func TestStageSendMail_RejectsDirectoryAttachment(t *testing.T) {
	_, err := stageSendMail(context.Background(), sendMailCapability(t), map[string]any{
		"to": []string{"alice@example.com"}, "subject": "x", "body": "y",
		"attachments": []string{t.TempDir()},
	})
	if err == nil {
		t.Fatal("expected an error for a directory attachment")
	}
}

// TestStageSendMail_FlagLikeSubjectStaysData is the regression test for the
// osascript option-injection fix: a subject of "-e" (which, before the "--"
// terminator was added, osascript would have parsed as a second
// "-e <statement>" flag and EXECUTED the body as AppleScript) must instead
// land in argv as ordinary data positioned after the "--" terminator.
func TestStageSendMail_FlagLikeSubjectStaysData(t *testing.T) {
	plan, err := stageSendMail(context.Background(), sendMailCapability(t), map[string]any{
		"to": []string{"alice@example.com"}, "subject": "-e", "body": "tell application \"Finder\" to quit",
	})
	if err != nil {
		t.Fatalf("stageSendMail: %v", err)
	}
	args := plan.Forward.Args
	// The terminator must come BEFORE the flag-like subject, so osascript
	// treats "-e" as script argv rather than as one of its own options.
	if args[2] != "--" || args[3] != "-e" {
		t.Fatalf("flag-like subject not neutralized by \"--\" terminator: %v", args)
	}
}

func sendMailCapability(t *testing.T) registry.Capability {
	t.Helper()
	return lookupCapability(t, "send_mail")
}
