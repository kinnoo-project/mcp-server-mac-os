// mutate_mail.go implements send_mail: composes and sends a real email via
// Mail.app's AppleScript dictionary.
//
// # AppleScript safety pattern (the injection-equivalent of the no-shell axiom)
//
// AppleScript source is a full scripting language, not a flag set — naively
// string-concatenating a subject/body into a script would be the AppleScript
// analogue of shell injection. sendMailAppleScript is therefore a FIXED,
// reviewed constant; every value the model supplies (recipients, subject,
// body) arrives as a plain argv element bound by AppleScript's own `on run
// argv` parameter — exactly the same "data, never code" discipline the
// project already requires for every subprocess argv (see
// .claude/rules/darwin-execution.md). osascript is invoked via
// exec.CommandContext like any other binary; there is still no shell
// anywhere in this path.
//
// # Why this is irreversible, not reversible
//
// There is no "unsend" for a real email. Unlike mkdir (rmdir) or
// write_setting (restore the prior value), send_mail's StagedPlan carries a
// nil Inverse — server.Execute's existing nil-inverse branch already renders
// "this change cannot be undone" without any new server-side machinery. The
// staged Preview shows the recipient(s)/subject/body VERBATIM (never
// summarized) plus an explicit irreversibility warning, since the human
// approving the `execute` call needs to see exactly what will be sent.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// sendMailAppleScript composes and immediately sends a message. Argv layout
// (fixed, not model-controlled): item 1 = subject, item 2 = body, items 3..N
// = one or more recipient addresses — chosen so the two fixed-position
// scalars come first and the variable-length recipient list trails, with no
// delimiter needed.
const sendMailAppleScript = `on run argv
	set theSubject to item 1 of argv
	set theBody to item 2 of argv
	set recipientList to items 3 thru (count of argv) of argv
	tell application "Mail"
		set newMessage to make new outgoing message with properties {subject:theSubject, content:theBody, visible:false}
		tell newMessage
			repeat with addr in recipientList
				make new to recipient at end of to recipients with properties {address:addr}
			end repeat
			send
		end tell
	end tell
end run`

// stageSendMail validates the message and stages it. Validation is light —
// non-empty recipients that look like addresses, a non-empty subject — not
// full RFC 5322 parsing; the goal is catching obvious mistakes before
// showing a preview, not being a mail-address validator.
func stageSendMail(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	to, _ := getStringList(in, "to")
	subject, _ := getString(in, "subject")
	body, _ := getString(in, "body")

	if len(to) == 0 {
		return nil, fmt.Errorf("send_mail: 'to' must contain at least one recipient")
	}
	for _, addr := range to {
		if !strings.Contains(addr, "@") {
			return nil, fmt.Errorf("send_mail: %q does not look like an email address", addr)
		}
	}
	if subject == "" {
		return nil, fmt.Errorf("send_mail: 'subject' is required")
	}

	args := append([]string{"-e", sendMailAppleScript, subject, body}, to...)

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Send an email:\n  To: %s\n  Subject: %s\n  Body:\n%s\n\n"+
				"This will send a REAL email via Mail.app immediately upon confirmation, and CANNOT be undone — there is no \"unsend.\"",
			strings.Join(to, ", "), subject, body,
		),
		Forward: Command{Binary: "osascript", Args: args},
		Inverse: nil, // irreversible: no inverse to offer
	}, nil
}
