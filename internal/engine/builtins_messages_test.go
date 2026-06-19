// builtins_messages_test.go tests the Messages reads' pure logic — SQL-literal
// escaping (the injection guard), handle matching, JSON decoding, limit capping,
// and rendering — with no subprocess and no real chat.db. The live query paths
// (and the contact_name resolution) are not exercised here; only validation that
// happens before any subprocess is.
package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// TestEscapeSQLLiteral is the SQL-injection regression: a value with quotes is
// neutralised by doubling them (the complete escaping a single-quoted SQLite
// literal needs), and a NUL byte is rejected rather than truncating the query.
func TestEscapeSQLLiteral(t *testing.T) {
	got, err := escapeSQLLiteral("' OR 1=1; DROP TABLE message;--")
	if err != nil {
		t.Fatalf("escapeSQLLiteral: %v", err)
	}
	if got != "'' OR 1=1; DROP TABLE message;--" {
		t.Errorf("quotes not doubled: %q", got)
	}
	if _, err := escapeSQLLiteral("a\x00b"); err == nil {
		t.Error("a NUL byte should be rejected")
	}
}

func TestPlausibleEmail(t *testing.T) {
	for _, ok := range []string{"a@b.com", "first.last@sub.example.co"} {
		if !plausibleEmail(ok) {
			t.Errorf("%q should be a plausible email", ok)
		}
	}
	for _, bad := range []string{"a@b", "no-at-sign", "has space@x.com", "quote'@x.com", "a@@b.com", ""} {
		if plausibleEmail(bad) {
			t.Errorf("%q should NOT be a plausible email", bad)
		}
	}
}

func TestHandleMatchClause(t *testing.T) {
	// Email → exact, escaped equality.
	clause, err := handleMatchClause("alice@example.com", true)
	if err != nil || clause != "h.id = 'alice@example.com'" {
		t.Errorf("email clause = %q, %v", clause, err)
	}
	// Phone → digit-suffix LIKE (digits only, injection-safe).
	clause, err = handleMatchClause("+1 (555) 123-4567", false)
	if err != nil || clause != "h.id LIKE '%15551234567'" {
		t.Errorf("phone clause = %q, %v", clause, err)
	}
	// Too-short phone is refused (a 3-digit suffix would match too much).
	if _, err := handleMatchClause("911", false); err == nil {
		t.Error("a too-short phone handle should be refused")
	}
	// An "email" that fails validation is refused, never inlined.
	if _, err := handleMatchClause("not'an'email", true); err == nil {
		t.Error("an invalid email handle should be refused")
	}
}

func TestMessageRow_JSONDecode(t *testing.T) {
	payload := `[{"is_from_me":0,"handle":"+15551234567","ts":"2026-06-19 08:00:00","text":"hi"},` +
		`{"is_from_me":1,"handle":"","ts":"2026-06-19 08:01:00","text":""}]`
	var rows []messageRow
	if err := json.Unmarshal([]byte(payload), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 2 || rows[0].Handle != "+15551234567" || rows[0].IsFromMe != 0 || rows[1].IsFromMe != 1 {
		t.Errorf("decoded rows wrong: %+v", rows)
	}
}

func TestCappedLimit(t *testing.T) {
	if got := cappedLimit(map[string]any{}, 20); got != 20 {
		t.Errorf("absent limit should use default, got %d", got)
	}
	if got := cappedLimit(map[string]any{"limit": 999}, 20); got != maxMessageLimit {
		t.Errorf("over-cap limit should clamp to %d, got %d", maxMessageLimit, got)
	}
	if got := cappedLimit(map[string]any{"limit": 5}, 20); got != 5 {
		t.Errorf("in-range limit should pass through, got %d", got)
	}
}

func TestPreviewText(t *testing.T) {
	if got := previewText("line1\nline2\t  line3"); got != "line1 line2 line3" {
		t.Errorf("multiline collapse = %q", got)
	}
	if got := previewText("   "); !strings.Contains(got, "no text") {
		t.Errorf("empty text should render a placeholder, got %q", got)
	}
	long := strings.Repeat("x", 200)
	if got := previewText(long); !strings.HasSuffix(got, "…") || len(got) > 130 {
		t.Errorf("long text should be truncated with an ellipsis, got len %d", len(got))
	}
}

func TestRenderMessages(t *testing.T) {
	out := renderMessages("Head:", []messageRow{
		{IsFromMe: 0, Handle: "+1555", Ts: "T1", Text: "hi"},
		{IsFromMe: 1, Handle: "", Ts: "T2", Text: "yo"},
	})
	for _, want := range []string{"Head:", "[T1] +1555: hi", "[T2] Me: yo"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q: %s", want, out)
		}
	}
}

// typedstreamBlob builds a minimal serialized-NSAttributedString blob carrying
// text, matching the real chat.db layout the extractor targets: a prefix (with
// no stray "NSString"), the class name, the version/`+` marker bytes, a length
// (single-byte, or 0x81-escaped for ≥128), the UTF-8 text, then trailing bytes.
func typedstreamBlob(text string) []byte {
	b := []byte("streamtyped\x81\xe8\x03\x84\x01@\x84NSObjectX NSString")
	b = append(b, 0x01, 0x94, 0x84, 0x01, '+')
	if n := len(text); n < 0x80 {
		b = append(b, byte(n))
	} else {
		b = append(b, 0x81, byte(n&0xff), byte(n>>8))
	}
	b = append(b, []byte(text)...)
	return append(b, 0x86, 0x84)
}

func TestExtractTypedstreamText(t *testing.T) {
	for _, want := range []string{
		"Only after school is open so no rush",
		"Hi",
		"emoji 😀 and unicode ñ",
		strings.Repeat("long ", 60), // forces the 0x81 two-byte length path
	} {
		if got := extractTypedstreamText(typedstreamBlob(want)); got != want {
			t.Errorf("extractTypedstreamText round-trip = %q, want %q", got, want)
		}
	}
	// No NSString marker, or truncated → empty, never a panic.
	if got := extractTypedstreamText([]byte("no marker here")); got != "" {
		t.Errorf("missing marker should yield \"\", got %q", got)
	}
	if got := extractTypedstreamText([]byte("NSString")); got != "" {
		t.Errorf("truncated blob should yield \"\", got %q", got)
	}
}

func TestMessageText(t *testing.T) {
	// Plain text present → used as-is, blob ignored.
	if got := messageText("plain text", hex.EncodeToString(typedstreamBlob("blob text"))); got != "plain text" {
		t.Errorf("plain text should win, got %q", got)
	}
	// Text empty → recovered from the attributedBody hex.
	if got := messageText("", hex.EncodeToString(typedstreamBlob("from the blob"))); got != "from the blob" {
		t.Errorf("blob fallback = %q, want \"from the blob\"", got)
	}
	// Both empty → empty (a genuine attachment-only message).
	if got := messageText("", ""); got != "" {
		t.Errorf("no text anywhere should be empty, got %q", got)
	}
	// Garbage hex must not error or panic, just yield "".
	if got := messageText("", "zzznothex"); got != "" {
		t.Errorf("invalid hex should yield \"\", got %q", got)
	}
}

// TestResolveMessageRecipient_ExactlyOne covers the shared exactly-one-of guard
// on the path that does NOT touch Contacts (handle supplied directly).
func TestResolveMessageRecipient_ExactlyOne(t *testing.T) {
	// Handle only → returned as-is, email flag set from the "@".
	raw, _, isEmail, err := resolveMessageRecipient(context.Background(), "x", map[string]any{"handle": "a@b.com"})
	if err != nil || raw != "a@b.com" || !isEmail {
		t.Errorf("handle-only resolution = %q, isEmail=%v, %v", raw, isEmail, err)
	}
	if _, _, _, err := resolveMessageRecipient(context.Background(), "x", map[string]any{}); err == nil {
		t.Error("expected an error when neither handle nor contact_name is given")
	}
	if _, _, _, err := resolveMessageRecipient(context.Background(), "x", map[string]any{"handle": "a@b.com", "contact_name": "Alice"}); err == nil {
		t.Error("expected an error when both handle and contact_name are given")
	}
}
