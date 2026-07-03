// builtins_mail_reads.go implements the read-only Mail.app operations that let
// the assistant answer "read me that email" — list_inbox and read_message —
// by driving Mail through its AppleScript dictionary via the hardened osascript
// seam in applescript.go, then formatting the delimited stdout into human text
// in Go.
//
// # Why these are AppleScript builtins, and how they relate to search_mail
//
// search_mail (builtins_mail.go) answers "find messages matching a term" by
// querying Spotlight's index of the on-disk Mail store. That is the right tool
// for full-text search, but it cannot list "the newest messages in my inbox"
// (there is no query term) nor return a specific message's body by identity.
// Those two needs are answered by Mail's own scripting dictionary, so — like the
// Notes and Calendar reads — the work is a FIXED AppleScript program plus result
// parsing, and lives here as an in-process builtin rather than a generic argv
// builder.
//
// # Guardrails shared with the other AppleScript reads
//
//   - Every model-supplied value (a mailbox name, a message id) reaches the
//     script strictly as DATA bound to `on run argv`, after the "--" terminator
//     that runOsascript inserts unconditionally (see applescript.go). A value
//     like a mailbox of "-e" is therefore inert, never an osascript flag.
//   - Field text (sender, subject) is flattened with _clean so a subject
//     containing a tab or newline cannot corrupt the one-row-per-line output the
//     Go parser relies on; dates render through _fmt as the stable
//     "YYYY-MM-DD HH:MM" the rest of the codebase parses.
//   - A denied Automation permission (the first call prompts for control of
//     Mail) surfaces as an actionable hint via mailScriptError rather than a bare
//     AppleScript error number.
//
// # Privacy
//
// read_message returns a message BODY — genuinely personal content. Its output
// is bounded by the shared compactOutput budget so a very long email cannot
// saturate the model's context, and the manifest description flags that it
// exposes private mail so a caller invokes it deliberately.
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// defaultInboxLimit / maxInboxLimit bound how many messages list_inbox returns:
// a default that keeps output readable and a hard ceiling a caller cannot exceed
// (the plan caps this operation at 50). The cap also bounds cost, since each row
// reads several per-message properties across the AppleScript bridge.
const (
	defaultInboxLimit = 20
	maxInboxLimit     = 50
)

// listInboxScript emits one tab-delimited metadata row per message, newest
// first, for the inbox (or a named mailbox):
//
//	id \t sender \t subject \t "YYYY-MM-DD HH:MM" date-received
//
// argv (after the "--" terminator): mailboxName, maxN. An empty mailboxName
// means the unified Inbox; a non-empty one selects the first mailbox with that
// name. maxN caps how many of the mailbox's messages are read.
//
// Ordering: Mail returns `messages of <mailbox>` in the mailbox's date order,
// newest first, so reading items 1..maxN yields the most recent messages
// without scanning the entire (potentially huge) mailbox. The Go side re-sorts
// the returned rows newest-first as a stable-display safety net. Helper calls
// are prefixed with `my` so they resolve to this script's _clean/_fmt handlers
// rather than Mail's dictionary.
const listInboxScript = asDateHelpers + `on run argv
	set mailboxName to item 1 of argv
	set maxN to (item 2 of argv) as integer
	set out to ""
	tell application "Mail"
		if mailboxName is "" then
			set theBox to inbox
		else
			set theBox to first mailbox whose name is mailboxName
		end if
		set msgs to messages of theBox
		set total to (count of msgs)
		if maxN > total then set maxN to total
		repeat with i from 1 to maxN
			set m to item i of msgs
			set out to out & (id of m) & tab & my _clean(sender of m) & tab & my _clean(subject of m) & tab & my _fmt(date received of m) & linefeed
		end repeat
	end tell
	return out
end run`

// readMessageScript returns one message's headers and full plain-text body,
// joined by asFieldSep (0x1f) so a multi-line body round-trips without being
// split. argv: id. The message is located by its unique id: the unified Inbox is
// tried first (the common case, cheapest), then every account's mailboxes are
// scanned as a fallback so a message filed outside the inbox is still readable.
// A bad id matches nothing and the script errors (non-zero exit), which
// runReadMessage turns into a clear "not found" message.
const readMessageScript = asDateHelpers + `on run argv
	set theId to (item 1 of argv) as integer
	set sep to (character id 31)
	tell application "Mail"
		set found to missing value
		try
			set matches to (messages of inbox whose id is theId)
			if (count of matches) > 0 then set found to item 1 of matches
		end try
		if found is missing value then
			repeat with acct in accounts
				repeat with mbx in mailboxes of acct
					try
						set matches to (messages of mbx whose id is theId)
						if (count of matches) > 0 then
							set found to item 1 of matches
							exit repeat
						end if
					end try
				end repeat
				if found is not missing value then exit repeat
			end repeat
		end if
		if found is missing value then error "no message with that id"
		set theSubject to my _clean(subject of found)
		set theSender to my _clean(sender of found)
		set theDate to my _fmt(date received of found)
		set theBody to (content of found)
		return theSubject & sep & theSender & sep & theDate & sep & theBody
	end tell
end run`

// inboxMsg is one parsed metadata row from listInboxScript.
type inboxMsg struct {
	id, sender, subject, received string
}

// runListInbox lists the most recent messages in the inbox (or a named
// mailbox), capped at limit.
func runListInbox(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	mailbox, _ := getString(in, "mailbox")
	limit := cappedInboxLimit(in)
	res, err := runOsascript(ctx, listInboxScript, mailbox, itoa(limit))
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", mailScriptError("list_inbox", res.Stderr)
	}
	msgs := parseInboxRows(res.Stdout)
	if len(msgs) == 0 {
		if mailbox != "" {
			return fmt.Sprintf("No messages found in mailbox %q.", mailbox), nil
		}
		return "No messages found in the inbox.", nil
	}
	msgs = sortInboxNewestFirst(msgs)
	scope := "inbox"
	if mailbox != "" {
		scope = fmt.Sprintf("mailbox %q", mailbox)
	}
	return renderInboxList(fmt.Sprintf("%d message(s) in %s, most recent first:", len(msgs), scope), msgs), nil
}

// runReadMessage returns one message's headers and full plain-text body, bounded
// by the shared output budget so a very long email can't saturate the model's
// context.
func runReadMessage(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	id, _ := getString(in, "id")
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("read_message: 'id' is required (from list_inbox)")
	}
	// A Mail message id is an integer, and readMessageScript coerces the argv value
	// with `as integer`. Validate that here so a non-numeric id (e.g. a value like
	// "id: 123" copied verbatim from a listing line) fails as a CLEAR caller error
	// rather than tripping the script's coercion and surfacing as a misleading
	// "grant Automation access" hint. This is a plain input check, not the injection
	// guard — the `--` terminator in runOsascript already makes the value inert data.
	if !isAllDigits(id) {
		return "", fmt.Errorf("read_message: 'id' %q is not a numeric message id (use list_inbox to get a current id)", id)
	}
	// Bound the digit count too: AppleScript's `as integer` overflows past a
	// 32-bit-ish range, and an overflow errors INSIDE the script, where it would
	// be misreported by the fallthrough below as an Automation-permission
	// problem. Real Mail ids fit comfortably in 19 digits, so anything longer is
	// certainly stale/garbled input, not a real id.
	if len(id) > 19 {
		return "", fmt.Errorf("read_message: 'id' %q is too long to be a Mail message id (use list_inbox to get a current id)", id)
	}
	res, err := runOsascript(ctx, readMessageScript, id)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		// A bad id makes the AppleScript error; distinguish that from a permission
		// failure so the caller knows to re-list rather than grant access.
		if strings.Contains(res.Stderr, "no message with that id") {
			return "", fmt.Errorf("read_message: no message found with id %q (use list_inbox to get a current id)", id)
		}
		return "", mailScriptError("read_message", res.Stderr)
	}
	// subject <sep> sender <sep> date <sep> body; SplitN keeps a body containing
	// anything (except the 0x1f separator, which mail text never does) intact.
	// osascript prints one trailing newline after the returned string; strip
	// exactly that one so a body that intentionally ends in blank lines is not
	// truncated (same rationale as read_note).
	parts := strings.SplitN(strings.TrimSuffix(res.Stdout, "\n"), asFieldSep, 4)
	if len(parts) != 4 {
		return "", fmt.Errorf("read_message: unexpected probe output for id %q", id)
	}
	subject, sender, date, body := parts[0], parts[1], parts[2], parts[3]
	if strings.TrimSpace(body) == "" {
		body = "(empty message)"
	}
	header := fmt.Sprintf("From: %s\nSubject: %s\nDate: %s", orUnknown(sender), orUntitled(subject), date)
	return compactOutput(fmt.Sprintf("%s\n\n%s", header, body)), nil
}

// parseInboxRows splits the tab-delimited rows from listInboxScript into
// inboxMsg values. A row with the wrong field count is skipped defensively so
// one odd record can't corrupt the whole listing (mirrors parseNoteRows).
func parseInboxRows(stdout string) []inboxMsg {
	var out []inboxMsg
	for _, row := range asRows(stdout) {
		f := strings.Split(row, "\t")
		if len(f) != 4 {
			continue
		}
		out = append(out, inboxMsg{id: f[0], sender: f[1], subject: f[2], received: f[3]})
	}
	return out
}

// sortInboxNewestFirst orders messages most-recently-received first. The
// received field is "YYYY-MM-DD HH:MM", which sorts lexicographically in
// chronological order, so a descending string sort is a descending time sort
// (same trick as sortAndCapNotes). The AppleScript already reads newest-first;
// this makes the display order deterministic regardless.
func sortInboxNewestFirst(msgs []inboxMsg) []inboxMsg {
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].received > msgs[j].received })
	return msgs
}

// renderInboxList formats message metadata under a heading, one message per
// block with its id on a second line (the id is what read_message consumes).
func renderInboxList(heading string, msgs []inboxMsg) string {
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for i, m := range msgs {
		fmt.Fprintf(&b, "%2d. %s  —  %s  (%s)\n      id: %s\n",
			i+1, orUnknown(m.sender), orUntitled(m.subject), m.received, m.id)
	}
	return b.String()
}

// cappedInboxLimit reads the "limit" parameter, applying the inbox default and
// the hard ceiling (mirrors cappedNoteLimit).
func cappedInboxLimit(in map[string]any) int {
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultInboxLimit
	}
	if limit > maxInboxLimit {
		limit = maxInboxLimit
	}
	return limit
}

// mailScriptError wraps an osascript failure with a hint about the most common
// cause — Automation access to Mail not yet granted — so the caller gets an
// actionable message instead of a bare AppleScript error number. Mirrors
// notesScriptError/calendarScriptError; the send_mail path establishes the same
// Mail Automation permission surface.
func mailScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: Mail automation failed (%s). If this is the first use, macOS may need you to grant this app access to Mail in System Settings → Privacy & Security → Automation", op, stderr)
}
