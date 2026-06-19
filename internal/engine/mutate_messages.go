// mutate_messages.go implements send_message: composing and sending an iMessage
// through Messages.app's AppleScript dictionary.
//
// Like send_mail this is IRREVERSIBLE — there is no unsend — so it stages a
// verbatim preview and a nil Inverse, gated by the existing `execute` step. The
// recipient and message text cross into AppleScript only as "--"-terminated
// `on run argv` data (osascriptCommand); the script body is a fixed constant.
//
// NOTE on the AppleScript: Messages' scripting `send` has varied in reliability
// across macOS releases — it is the one genuinely version-sensitive piece here
// and is verified by manual smoke test, not automated execution (sending a real
// message is irreversible). See docs/issues/note-imessage-applescript-send.md.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// sendIMessageScript sends theText (argv item 2) to the iMessage buddy for
// theHandle (argv item 1). Both are bound as data; the script is fixed.
const sendIMessageScript = `on run argv
	set theHandle to item 1 of argv
	set theText to item 2 of argv
	tell application "Messages"
		set targetService to 1st service whose service type = iMessage
		set targetBuddy to buddy theHandle of targetService
		send theText to targetBuddy
	end tell
end run`

// stageSendMessage validates the recipient and text and stages an irreversible
// send. The recipient may be an explicit handle or a contact_name resolved via
// Contacts (reusing resolveMessageRecipient, shared with read_conversation).
func stageSendMessage(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	text, _ := getString(in, "text")
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("send_message: 'text' is required")
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

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"The following iMessage will be sent to %s:\n\n%s\n\nThis cannot be undone once sent — there is no \"unsend.\" Send it?",
			target, text,
		),
		Forward: osascriptCommand(sendIMessageScript, handle, text),
		Inverse: nil, // irreversible: a sent message has no undo
	}, nil
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
