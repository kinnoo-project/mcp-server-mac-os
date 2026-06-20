// mutate_messages_test.go tests send_message's validation, argv construction,
// and preview.
//
// SAFETY: no test executes a StagedPlan, so no real iMessage is ever sent. The
// contact_name path runs a live Contacts probe, so only the explicit-handle path
// and pre-resolution validation are unit-tested.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	// ["-e", sendIMessageScript, "--", handle, text, attachmentCount]
	if len(a) != 6 || a[1] != sendIMessageScript {
		t.Fatalf("unexpected argv: %v", a)
	}
	if a[2] != "--" {
		t.Errorf("missing osascript terminator at index 2: %v", a)
	}
	if a[3] != "+15551234567" || a[4] != "on my way" || a[5] != "0" {
		t.Errorf("handle/text/count not in expected positions (handle should be canonicalized): %v", a[3:])
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

// TestStageSendMessage_WithAttachments verifies an attachment-bearing send packs
// the count and each existing file path after the handle/text, and that the
// preview names the file(s).
func TestStageSendMessage_WithAttachments(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "Leah.png")
	if err := os.WriteFile(img, []byte("\x89PNG"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "5551234567", "text": "here you go", "attachments": []string{img},
	})
	if err != nil {
		t.Fatalf("stageSendMessage: %v", err)
	}
	a := plan.Forward.Args
	// ["-e", script, "--", handle, text, "1", img]
	if len(a) != 7 || a[5] != "1" || a[6] != img {
		t.Fatalf("attachment count/path not in expected positions: %v", a)
	}
	if !strings.Contains(plan.Preview, "Leah.png") {
		t.Errorf("preview should name the attachment: %s", plan.Preview)
	}
}

// TestStageSendMessage_AttachmentsOnly verifies a file may be sent with no text.
func TestStageSendMessage_AttachmentsOnly(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "pic.jpg")
	if err := os.WriteFile(img, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "5551234567", "attachments": []string{img},
	})
	if err != nil {
		t.Fatalf("stageSendMessage: %v", err)
	}
	a := plan.Forward.Args
	// text argument is empty, count is "1"
	if a[4] != "" || a[5] != "1" || a[6] != img {
		t.Fatalf("attachments-only argv unexpected: %v", a)
	}
	if !strings.Contains(plan.Preview, "pic.jpg") {
		t.Errorf("preview should name the attachment: %s", plan.Preview)
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
		"no text or attachment": {"handle": "5551234567"},
		"no recipient":          {"text": "hi"},
		"both recipients":       {"handle": "5551234567", "contact_name": "Alice", "text": "hi"},
		"bad phone handle":      {"handle": "not-a-number", "text": "hi"},
		"bad email handle":      {"handle": "broken@", "text": "hi"},
		"missing attachment":    {"handle": "5551234567", "attachments": []string{"/no/such/file.png"}},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := stageSendMessage(context.Background(), sendMessageCapability(t), params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

// TestStageSendMessage_RejectsDirAttachment verifies a directory path is refused
// (you can't attach a folder).
func TestStageSendMessage_RejectsDirAttachment(t *testing.T) {
	dir := t.TempDir()
	if _, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "5551234567", "attachments": []string{dir},
	}); err == nil {
		t.Error("expected an error when an attachment path is a directory")
	}
}

// TestStageSendMessage_RejectsNonRegularAttachment verifies a non-regular
// filesystem object (here a FIFO) is refused at stage time rather than allowed
// through to fail opaquely in AppleScript: attachments must be regular files, not
// merely "not directories".
func TestStageSendMessage_RejectsNonRegularAttachment(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create FIFO on this platform: %v", err)
	}
	if _, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "5551234567", "attachments": []string{fifo},
	}); err == nil {
		t.Error("expected an error when an attachment path is not a regular file")
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
