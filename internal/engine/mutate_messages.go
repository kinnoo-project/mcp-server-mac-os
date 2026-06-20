// mutate_messages.go implements send_message: composing and sending an iMessage
// (optionally with file attachments) through Messages.app's AppleScript
// dictionary.
//
// Like send_mail this is IRREVERSIBLE — there is no unsend — so it stages a
// verbatim preview and a nil Inverse, gated by the existing `execute` step. The
// recipient, message text, and attachment paths cross into AppleScript only as
// "--"-terminated `on run argv` data (osascriptCommand); the script body is a
// fixed constant.
//
// NOTE on the AppleScript: Messages' scripting `send` has varied in reliability
// across macOS releases — it is the one genuinely version-sensitive piece here
// and is verified by manual smoke test, not automated execution (sending a real
// message is irreversible). See docs/issues/note-imessage-applescript-send.md.
package engine

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// sendIMessageScript sends optional attachments and optional text to the
// iMessage buddy for theHandle. Argv layout (fixed, not model-controlled):
// item 1 = handle, item 2 = text, item 3 = the attachment count N (as a
// string), and items 4..3+N = attachment POSIX paths. The explicit count lets
// a variable-length attachment list share one flat argv with the leading
// scalar fields without inventing a delimiter a path might contain — the same
// pattern send_mail uses.
//
// Attachments are sent first, then any non-empty text, mirroring how the
// Messages UI presents an image above its caption. Each `send` is a separate
// iMessage; AppleScript has no single "image-with-caption" send.
const sendIMessageScript = `on run argv
	set theHandle to item 1 of argv
	set theText to item 2 of argv
	set attachmentCount to (item 3 of argv) as integer
	tell application "Messages"
		set targetService to 1st service whose service type = iMessage
		set targetBuddy to buddy theHandle of targetService
		if attachmentCount > 0 then
			repeat with i from 4 to (3 + attachmentCount)
				set theAttachment to (POSIX file (item i of argv)) as alias
				send theAttachment to targetBuddy
			end repeat
		end if
		if theText is not "" then send theText to targetBuddy
	end tell
end run`

// stageSendMessage validates the recipient, text, and attachments and stages an
// irreversible send. The recipient may be an explicit handle or a contact_name
// resolved via Contacts (reusing resolveMessageRecipient, shared with
// read_conversation). Either text or at least one attachment must be present —
// an empty message with no files is rejected.
func stageSendMessage(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	text, _ := getString(in, "text")
	text = strings.TrimSpace(text)
	attachments, _ := getStringList(in, "attachments")

	if text == "" && len(attachments) == 0 {
		return nil, fmt.Errorf("send_message: provide 'text', 'attachments', or both — nothing to send")
	}

	// Validate attachment paths for real before staging: each must exist and be
	// a regular file. This is the same read-only-probe-before-staging discipline
	// send_mail uses, and it also guarantees the script's `as alias` coercion
	// (which fails on a missing file) won't blow up at commit time. We require a
	// regular file specifically — not merely "not a directory" — so non-regular
	// objects (device nodes, sockets, FIFOs) are rejected here with a clear error
	// rather than failing opaquely in AppleScript at commit time.
	for _, path := range attachments {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("send_message: attachment %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("send_message: attachment %q is not a regular file", path)
		}
	}

	raw, display, isEmail, err := resolveMessageRecipient(ctx, "send_message", in)
	if err != nil {
		return nil, err
	}
	handle, err := validateSendHandle(raw, isEmail)
	if err != nil {
		return nil, fmt.Errorf("send_message: %w", err)
	}

	// Name the recipient clearly: "Alice (+15551234567)" when resolved from a
	// contact, or just the handle when supplied directly.
	target := handle
	if display != "" && display != raw && display != handle {
		target = fmt.Sprintf("%s (%s)", display, handle)
	}

	// Data arguments bound to the script's `on run argv`: handle, text, the
	// attachment count, then the attachment paths. osascriptCommand inserts the
	// "--" end-of-options terminator ahead of these, so a value like text="-e"
	// reaches the script as data, never as an osascript flag (the
	// option-injection guard documented in applescript.go and CLAUDE.md §4).
	dataArgs := append([]string{handle, text, strconv.Itoa(len(attachments))}, attachments...)

	return &StagedPlan{
		Preview: messageSendPreview(target, text, attachments),
		Forward: osascriptCommand(sendIMessageScript, dataArgs...),
		Inverse: nil, // irreversible: a sent message has no undo
	}, nil
}

// messageSendPreview renders the verbatim confirmation a human approves before
// the send fires. It shows the message text in full (never summarized) and the
// attachment filenames, adapting its wording to whether text, files, or both
// are being sent.
func messageSendPreview(target, text string, attachments []string) string {
	var b strings.Builder
	switch {
	case text != "" && len(attachments) > 0:
		fmt.Fprintf(&b, "The following iMessage and %d attachment(s) will be sent to %s:\n\n%s\n\nAttachments: %s\n",
			len(attachments), target, text, strings.Join(baseNames(attachments), ", "))
	case len(attachments) > 0:
		fmt.Fprintf(&b, "The following %d attachment(s) will be sent as an iMessage to %s:\n\nAttachments: %s\n",
			len(attachments), target, strings.Join(baseNames(attachments), ", "))
	default:
		fmt.Fprintf(&b, "The following iMessage will be sent to %s:\n\n%s\n", target, text)
	}
	b.WriteString("\nThis cannot be undone once sent — there is no \"unsend.\" Send it?")
	return b.String()
}

// validateSendHandle returns the handle string to address the message to: a
// checked email as-is, or a phone number reduced to its canonical digits (which
// also rejects anything that isn't a plausible number).
func validateSendHandle(raw string, isEmail bool) (string, error) {
	if isEmail {
		if !plausibleEmail(raw) {
			return "", fmt.Errorf("%q does not look like a valid email handle", raw)
		}
		return raw, nil
	}
	return canonicalizePhoneNumber(raw)
}
