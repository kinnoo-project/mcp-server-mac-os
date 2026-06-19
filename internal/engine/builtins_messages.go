// builtins_messages.go implements the read-only Messages operations —
// check_messages, search_messages, read_conversation, list_conversations — by
// querying the local Messages SQLite database (~/Library/Messages/chat.db) with
// `sqlite3 -readonly -json`.
//
// # Why sqlite3, read-only, JSON
//
// Messages.app's AppleScript dictionary offers no usable read access to message
// history; the history lives in chat.db. We open it `-readonly` (defense in
// depth: a read capability can never write) and ask for `-json` output so a
// message body containing tabs/newlines/quotes can't corrupt parsing — Go
// decodes it with encoding/json (stdlib, no new dependency). chat.db is
// protected by macOS Full Disk Access; until the host process is granted it,
// sqlite3 fails to open the file, which we turn into an actionable hint.
//
// # SQL-injection safety (the key guardrail for this domain)
//
// Every query is a FIXED template. The only variable pieces are:
//   - a numeric LIMIT, formatted from a Go int with %d (never a string), and
//   - a handle or search term, which is either VALIDATED to a form that cannot
//     contain a quote (a phone reduced to digits, a checked email) or embedded
//     via escapeSQLLiteral (doubling ” — the complete and only escaping a
//     single-quoted SQLite string literal needs).
//
// This is the SQL analogue of the osascript "--" terminator and the open
// URL-scheme guard. The send_message mutator lives in mutate_messages.go.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// Read limits: a readable default and a hard ceiling, like the other app reads.
const (
	defaultMessageLimit      = 20
	defaultConversationDepth = 30
	maxMessageLimit          = 50
)

// baseMessageSelect is the shared SELECT for check/search/read_conversation. It
// projects each message as the four JSON fields messageRow decodes, converting
// Apple's date (nanoseconds since 2001-01-01 UTC; 978307200 is the offset to the
// Unix epoch) into a local-time string in SQL so Go needs no date math. COALESCE
// turns NULL text/handle (attachment-only rows, unknown senders) into "".
const baseMessageSelect = `SELECT m.is_from_me AS is_from_me, ` +
	`COALESCE(h.id,'') AS handle, ` +
	`datetime(m.date/1000000000 + 978307200,'unixepoch','localtime') AS ts, ` +
	`COALESCE(m.text,'') AS text ` +
	`FROM message m LEFT JOIN handle h ON m.handle_id = h.ROWID`

// messageRow is one decoded row of baseMessageSelect's JSON output.
type messageRow struct {
	IsFromMe int    `json:"is_from_me"`
	Handle   string `json:"handle"`
	Ts       string `json:"ts"`
	Text     string `json:"text"`
}

// conversationRow is one decoded row of the list_conversations query.
type conversationRow struct {
	Chat string `json:"chat"`
	Ts   string `json:"ts"`
	Text string `json:"text"`
}

// chatDBPath returns the absolute path to the Messages database.
func chatDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Messages", "chat.db"), nil
}

// runMessagesQuery runs a fixed SQL statement against chat.db read-only and
// decodes the `-json` output into dest (a pointer to a slice). Zero rows make
// sqlite3 print nothing, which is treated as an empty result, not an error.
func runMessagesQuery(ctx context.Context, sql string, dest any) error {
	db, err := chatDBPath()
	if err != nil {
		return err
	}
	bin, err := policy.ResolveBinary("sqlite3")
	if err != nil {
		return err
	}
	res, err := runCommand(ctx, bin, "-readonly", "-json", db, sql)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return messagesDBError(res.Stderr)
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		return nil // no rows
	}
	if err := json.Unmarshal([]byte(out), dest); err != nil {
		return fmt.Errorf("messages: could not parse sqlite output: %w", err)
	}
	return nil
}

// runCheckMessages lists the most recent messages, optionally only unread ones
// received from others.
func runCheckMessages(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	limit := cappedLimit(in, defaultMessageLimit)
	where := ""
	if getBool(in, "unread_only") {
		where = " WHERE m.is_from_me = 0 AND m.is_read = 0"
	}
	sql := fmt.Sprintf("%s%s ORDER BY m.date DESC LIMIT %d", baseMessageSelect, where, limit)

	var rows []messageRow
	if err := runMessagesQuery(ctx, sql, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		if where != "" {
			return "No unread messages.", nil
		}
		return "No messages found.", nil
	}
	return renderMessages(fmt.Sprintf("%d most recent message(s):", len(rows)), rows), nil
}

// runSearchMessages returns messages whose text matches a term, newest first.
func runSearchMessages(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	query, _ := getString(in, "query")
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("search_messages: 'query' is required")
	}
	esc, err := escapeSQLLiteral(query)
	if err != nil {
		return "", fmt.Errorf("search_messages: %w", err)
	}
	limit := cappedLimit(in, defaultMessageLimit)
	sql := fmt.Sprintf("%s WHERE m.text IS NOT NULL AND m.text LIKE '%%' || '%s' || '%%' ORDER BY m.date DESC LIMIT %d",
		baseMessageSelect, esc, limit)

	var rows []messageRow
	if err := runMessagesQuery(ctx, sql, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return fmt.Sprintf("No messages found matching %q.", query), nil
	}
	return renderMessages(fmt.Sprintf("%d message(s) matching %q:", len(rows), query), rows), nil
}

// runReadConversation shows the recent history with one person, chronologically.
func runReadConversation(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	raw, display, isEmail, err := resolveMessageRecipient(ctx, "read_conversation", in)
	if err != nil {
		return "", err
	}
	clause, err := handleMatchClause(raw, isEmail)
	if err != nil {
		return "", fmt.Errorf("read_conversation: %w", err)
	}
	limit := cappedLimit(in, defaultConversationDepth)
	sql := fmt.Sprintf("%s WHERE %s ORDER BY m.date DESC LIMIT %d", baseMessageSelect, clause, limit)

	var rows []messageRow
	if err := runMessagesQuery(ctx, sql, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return fmt.Sprintf("No messages found with %s.", display), nil
	}
	// The query takes the newest N; reverse to read oldest→newest like a chat.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return renderMessages(fmt.Sprintf("Conversation with %s (%d message(s), oldest first):", display, len(rows)), rows), nil
}

// runListConversations lists recent chats with each one's last message. The
// window function picks the most recent message per chat.
func runListConversations(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	limit := cappedLimit(in, defaultMessageLimit)
	sql := fmt.Sprintf(`SELECT chat, ts, text FROM (`+
		`SELECT COALESCE(NULLIF(c.display_name,''), c.chat_identifier) AS chat, `+
		`datetime(m.date/1000000000 + 978307200,'unixepoch','localtime') AS ts, `+
		`COALESCE(m.text,'') AS text, `+
		`ROW_NUMBER() OVER (PARTITION BY c.ROWID ORDER BY m.date DESC) AS rn `+
		`FROM chat c `+
		`JOIN chat_message_join cmj ON cmj.chat_id = c.ROWID `+
		`JOIN message m ON m.ROWID = cmj.message_id`+
		`) WHERE rn = 1 ORDER BY ts DESC LIMIT %d`, limit)

	var rows []conversationRow
	if err := runMessagesQuery(ctx, sql, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "No conversations found.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d recent conversation(s):\n", len(rows))
	for _, c := range rows {
		fmt.Fprintf(&b, "  [%s] %s: %s\n", c.Ts, orUnknown(c.Chat), previewText(c.Text))
	}
	return b.String(), nil
}

// resolveMessageRecipient validates that exactly one of handle/contact_name is
// supplied and, for a contact_name, resolves it to a number via the phone
// domain's resolver (reused for the same ambiguity-refusal behavior). It returns
// the raw handle, a display label, and whether the handle is an email.
func resolveMessageRecipient(ctx context.Context, op string, in map[string]any) (raw, display string, isEmail bool, err error) {
	handle, _ := getString(in, "handle")
	contactName, _ := getString(in, "contact_name")
	handle = strings.TrimSpace(handle)
	contactName = strings.TrimSpace(contactName)

	if (handle == "") == (contactName == "") {
		return "", "", false, fmt.Errorf("%s: provide exactly one of 'handle' or 'contact_name'", op)
	}
	if contactName != "" {
		num, name, rerr := resolveSingleNumber(ctx, contactName)
		if rerr != nil {
			return "", "", false, rerr
		}
		return num, name, false, nil
	}
	return handle, handle, strings.Contains(handle, "@"), nil
}

// handleMatchClause builds the WHERE clause that selects one conversation. An
// email is matched exactly (validated, then escaped). A phone is reduced to its
// digits and matched as a SUFFIX of the stored handle (digits are injection-safe
// and a suffix match tolerates the stored E.164 form, e.g. "+15551234567",
// differing from how the user typed it).
func handleMatchClause(raw string, isEmail bool) (string, error) {
	if isEmail {
		if !plausibleEmail(raw) {
			return "", fmt.Errorf("%q does not look like a valid email handle", raw)
		}
		esc, err := escapeSQLLiteral(raw)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("h.id = '%s'", esc), nil
	}
	canonical, err := canonicalizePhoneNumber(raw)
	if err != nil {
		return "", err
	}
	digits := strings.TrimPrefix(canonical, "+")
	if len(digits) < 7 {
		return "", fmt.Errorf("phone handle %q has too few digits to match a conversation reliably", raw)
	}
	// digits is guaranteed [0-9], so it is safe to inline directly.
	return fmt.Sprintf("h.id LIKE '%%%s'", digits), nil
}

// escapeSQLLiteral makes a string safe to embed inside a single-quoted SQLite
// literal by doubling embedded quotes (the only metacharacter such a literal
// has). A NUL byte is rejected outright rather than truncating the query.
func escapeSQLLiteral(s string) (string, error) {
	if strings.IndexByte(s, 0) >= 0 {
		return "", fmt.Errorf("value contains a NUL byte")
	}
	return strings.ReplaceAll(s, "'", "''"), nil
}

// emailPattern is a deliberately strict plausibility check (not full RFC 5322):
// it forbids whitespace and quotes outright, which is what matters for safety.
var emailPattern = regexp.MustCompile(`^[^@\s'"]+@[^@\s'"]+\.[^@\s'"]+$`)

// plausibleEmail reports whether s looks like an email handle safe to match on.
func plausibleEmail(s string) bool { return emailPattern.MatchString(s) }

// cappedLimit reads the "limit" parameter, applying the given default and the
// shared hard ceiling.
func cappedLimit(in map[string]any, def int) int {
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = def
	}
	if limit > maxMessageLimit {
		limit = maxMessageLimit
	}
	return limit
}

// renderMessages formats message rows under a heading, labeling outgoing
// messages "Me" and unknown senders "(unknown)".
func renderMessages(heading string, rows []messageRow) string {
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for _, r := range rows {
		who := orUnknown(r.Handle)
		if r.IsFromMe == 1 {
			who = "Me"
		}
		fmt.Fprintf(&b, "  [%s] %s: %s\n", r.Ts, who, previewText(r.Text))
	}
	return b.String()
}

// previewText collapses a (possibly multi-line) message body to a single tidy
// line for listing, so one long or multi-line message can't dominate the output.
func previewText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "(no text — attachment or unsupported message)"
	}
	const max = 120
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// orUnknown renders an empty handle/chat name as a clear placeholder.
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unknown)"
	}
	return s
}

// messagesDBError turns a sqlite3 failure into an actionable message. The most
// common cause by far is that the host process has not been granted Full Disk
// Access, which surfaces as an "authorization denied" / "unable to open
// database" error when sqlite3 tries to open chat.db.
func messagesDBError(stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if strings.Contains(stderr, "authorization denied") || strings.Contains(stderr, "unable to open database") {
		return fmt.Errorf("messages: cannot open the Messages database (%s). Grant this app Full Disk Access in System Settings → Privacy & Security → Full Disk Access, then try again", stderr)
	}
	if stderr == "" {
		stderr = "sqlite3 reported a non-zero exit with no detail"
	}
	return fmt.Errorf("messages: reading the Messages database failed (%s)", stderr)
}
