// mutate_messages_test.go tests send_message's validation, argv construction,
// and preview.
//
// SAFETY: no test executes a StagedPlan, so no real iMessage is ever sent. The
// contact_name path runs a live Contacts probe, so only the explicit-handle path
// and pre-resolution validation are unit-tested.
package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"mcp-server-mac-os/internal/registry"
)

func sendMessageCapability(t *testing.T) registry.Capability {
	return lookupCapability(t, "send_message")
}

// withMessagesSandbox redirects attachment staging to a temporary directory for
// the duration of a test, so a successful attachment send copies into the temp
// dir instead of the real Messages sandbox container. It returns that directory
// and restores the previous value on cleanup.
func withMessagesSandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := messagesSandboxTmpDir
	messagesSandboxTmpDir = dir
	t.Cleanup(func() { messagesSandboxTmpDir = prev })
	return dir
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
// the count and each path after the handle/text, that the path sent is a copy
// STAGED INTO THE SANDBOX (not the original, unreadable-to-Messages location)
// while preserving the basename and byte content, and that the preview names the
// original file.
func TestStageSendMessage_WithAttachments(t *testing.T) {
	sandbox := withMessagesSandbox(t)
	dir := t.TempDir()
	img := filepath.Join(dir, "Leah.png")
	content := []byte("\x89PNG-original-bytes")
	if err := os.WriteFile(img, content, 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "5551234567", "text": "here you go", "attachments": []string{img},
	})
	if err != nil {
		t.Fatalf("stageSendMessage: %v", err)
	}
	a := plan.Forward.Args
	// ["-e", script, "--", handle, text, "1", stagedPath]
	if len(a) != 7 || a[5] != "1" {
		t.Fatalf("attachment count/path not in expected positions: %v", a)
	}
	staged := a[6]
	if staged == img {
		t.Errorf("attachment must be staged into the sandbox, not sent from its original path: %s", staged)
	}
	if !strings.HasPrefix(staged, sandbox) {
		t.Errorf("staged path %q not under the sandbox dir %q", staged, sandbox)
	}
	if filepath.Base(staged) != "Leah.png" {
		t.Errorf("staged copy should preserve the original basename: %s", staged)
	}
	if got, err := os.ReadFile(staged); err != nil || !bytes.Equal(got, content) {
		t.Errorf("staged copy content mismatch (err=%v): %q", err, got)
	}
	if !strings.Contains(plan.Preview, "Leah.png") {
		t.Errorf("preview should name the attachment: %s", plan.Preview)
	}
}

// TestStageSendMessage_AttachmentsOnly verifies a file may be sent with no text,
// and is likewise staged into the sandbox.
func TestStageSendMessage_AttachmentsOnly(t *testing.T) {
	sandbox := withMessagesSandbox(t)
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
	// text argument is empty, count is "1", and the path is the staged copy.
	if a[4] != "" || a[5] != "1" || !strings.HasPrefix(a[6], sandbox) || filepath.Base(a[6]) != "pic.jpg" {
		t.Fatalf("attachments-only argv unexpected: %v", a)
	}
	if !strings.Contains(plan.Preview, "pic.jpg") {
		t.Errorf("preview should name the attachment: %s", plan.Preview)
	}
}

// TestStageSendMessage_MultipleAttachmentsSameBasename verifies two attachments
// sharing a basename are staged into separate subdirectories so neither
// overwrites the other, and both reach the argv with their original name.
func TestStageSendMessage_MultipleAttachmentsSameBasename(t *testing.T) {
	withMessagesSandbox(t)
	d1, d2 := t.TempDir(), t.TempDir()
	p1 := filepath.Join(d1, "report.pdf")
	p2 := filepath.Join(d2, "report.pdf")
	if err := os.WriteFile(p1, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "5551234567", "attachments": []string{p1, p2},
	})
	if err != nil {
		t.Fatalf("stageSendMessage: %v", err)
	}
	a := plan.Forward.Args
	// ["-e", script, "--", handle, "", "2", staged1, staged2]
	if len(a) != 8 || a[5] != "2" {
		t.Fatalf("unexpected argv for two attachments: %v", a)
	}
	if a[6] == a[7] {
		t.Fatalf("same-basename attachments collided onto one path: %v", a[6:])
	}
	if got, _ := os.ReadFile(a[6]); !bytes.Equal(got, []byte("first")) {
		t.Errorf("first attachment content wrong: %q", got)
	}
	if got, _ := os.ReadFile(a[7]); !bytes.Equal(got, []byte("second")) {
		t.Errorf("second attachment content wrong: %q", got)
	}
}

// TestStageSendMessage_SandboxContainerAbsent verifies that if Messages' sandbox
// container cannot be found (e.g. the hardcoded bundle id no longer matches),
// an attachment send is refused up front with a clear error rather than silently
// failing to transmit.
func TestStageSendMessage_SandboxContainerAbsent(t *testing.T) {
	prev := messagesSandboxTmpDir
	messagesSandboxTmpDir = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { messagesSandboxTmpDir = prev })

	dir := t.TempDir()
	img := filepath.Join(dir, "x.png")
	if err := os.WriteFile(img, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := stageSendMessage(context.Background(), sendMessageCapability(t), map[string]any{
		"handle": "5551234567", "attachments": []string{img},
	}); err == nil {
		t.Error("expected an error when the Messages sandbox container is absent")
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

// TestSweepStaleStagingDirs verifies the reclamation sweep deletes staging
// directories older than the cutoff while leaving fresh ones (and the just-made
// copy) intact — so a stale send's scratch is reclaimed but an in-flight upload's
// file is never pulled out from under it.
func TestSweepStaleStagingDirs(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "stale")
	fresh := filepath.Join(root, "fresh")
	for _, d := range []string{stale, fresh} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate the stale dir well beyond the max age.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	sweepStaleStagingDirs(root, time.Hour)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale staging dir should have been swept, got err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh staging dir should have survived the sweep: %v", err)
	}
}

// TestSweepStaleStagingDirs_MissingRootIsNoop verifies the sweep is a safe no-op
// when the staging root has never been created (the first-ever send), since a
// failed sweep must never block a send.
func TestSweepStaleStagingDirs_MissingRootIsNoop(t *testing.T) {
	sweepStaleStagingDirs(filepath.Join(t.TempDir(), "never-created"), time.Hour)
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
