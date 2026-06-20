// mutate_mail.go implements send_mail: composes and sends a real email via
// Mail.app's AppleScript dictionary.
//
// # AppleScript safety pattern (the injection-equivalent of the no-shell axiom)
//
// AppleScript source is a full scripting language, not a flag set — naively
// string-concatenating a subject/body into a script would be the AppleScript
// analogue of shell injection. sendMailAppleScript is therefore a FIXED,
// reviewed constant; every value the model supplies (recipients, subject,
// body, attachment paths) arrives as a plain argv element bound by
// AppleScript's own `on run argv` parameter — exactly the same "data, never
// code" discipline the project already requires for every subprocess argv
// (see .claude/rules/darwin-execution.md). osascript is invoked via
// exec.CommandContext like any other binary; there is still no shell
// anywhere in this path.
//
// # Why this is irreversible, not reversible
//
// There is no "unsend" for a real email. Unlike mkdir (rmdir) or
// write_setting (restore the prior value), send_mail's StagedPlan carries a
// nil Inverse — server.Execute's existing nil-inverse branch already renders
// "this change cannot be undone" without any new server-side machinery. The
// staged Preview shows the recipient(s)/subject/body/attachments VERBATIM
// (never summarized), since the human approving the `execute` call needs to
// see exactly what will be sent.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// sendMailAppleScript composes and immediately sends a message. Argv layout
// (fixed, not model-controlled): item 1 = subject, item 2 = body, item 3 =
// the recipient count N (as a string), items 4..3+N = recipient addresses,
// and any remaining items = attachment paths. The explicit count is what
// lets two variable-length lists (recipients, attachments) share one flat
// argv unambiguously, without inventing a delimiter character that some
// recipient or path might legitimately contain.
const sendMailAppleScript = `on run argv
	set theSubject to item 1 of argv
	set theBody to item 2 of argv
	set recipientCount to (item 3 of argv) as integer
	set recipientList to {}
	if recipientCount > 0 then set recipientList to items 4 thru (3 + recipientCount) of argv
	set attachmentList to {}
	if (count of argv) > (3 + recipientCount) then set attachmentList to items (4 + recipientCount) thru (count of argv) of argv
	tell application "Mail"
		set newMessage to make new outgoing message with properties {subject:theSubject, content:theBody, visible:false}
		tell newMessage
			repeat with addr in recipientList
				make new to recipient at end of to recipients with properties {address:addr}
			end repeat
			repeat with attPath in attachmentList
				make new attachment with properties {file name:(POSIX file attPath)} at after the last paragraph
			end repeat
			send
		end tell
	end tell
end run`

// stageSendMail validates the message and stages it. Address/subject
// validation is light — non-empty recipients that look like addresses, a
// non-empty subject — not full RFC 5322 parsing; the goal is catching
// obvious mistakes before showing a preview, not being a mail-address
// validator. Attachment paths ARE checked for real: each must exist and be
// a regular file, the same read-only-probe-before-staging discipline mkdir
// uses for its target path.
func stageSendMail(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	to, _ := getStringList(in, "to")
	subject, _ := getString(in, "subject")
	body, _ := getString(in, "body")
	attachments, _ := getStringList(in, "attachments")

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
	for _, path := range attachments {
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("send_mail: attachment %q: %w", path, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("send_mail: attachment %q is a directory, not a file", path)
		}
	}

	// Data arguments bound to the script's `on run argv`. osascriptCommand
	// inserts the "--" end-of-options terminator ahead of these, so a value like
	// subject="-e" reaches the script as data, never as an osascript flag (the
	// option-injection guard documented in applescript.go and CLAUDE.md §4).
	dataArgs := append([]string{subject, body, strconv.Itoa(len(to))}, to...)
	dataArgs = append(dataArgs, attachments...)

	var preview strings.Builder
	fmt.Fprintf(&preview, "The following email will be sent to %s:\n\n", strings.Join(to, ", "))
	fmt.Fprintf(&preview, "Subject: %s\nBody:\n%s\n", subject, body)
	if len(attachments) > 0 {
		fmt.Fprintf(&preview, "\nAttachments: %s\n", strings.Join(baseNames(attachments), ", "))
	}
	preview.WriteString("\nThis cannot be undone once sent — there is no \"unsend.\" Send this email?")

	return &StagedPlan{
		Preview: preview.String(),
		Forward: osascriptCommand(sendMailAppleScript, dataArgs...),
		Inverse: nil, // irreversible: no inverse to offer
	}, nil
}

// baseNames renders each attachment path's filename only, so the preview
// stays readable when a path is long, without hiding which files are
// actually being attached.
func baseNames(paths []string) []string {
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return names
}
