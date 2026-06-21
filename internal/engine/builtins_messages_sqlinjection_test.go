// builtins_messages_sqlinjection_test.go is part of the production security gate
// (see docs/TESTS.md). The escapeSQLLiteral unit test proves the escaper doubles
// quotes and rejects NUL bytes; this file proves the END-TO-END claim that
// matters: feeding an injection payload to the real search path against a real
// (hermetic, throwaway) Messages database neither executes the injected SQL nor
// returns rows it should not.
//
// The database is a temp chat.db with the minimal schema the message queries
// touch, created under a redirected $HOME so the developer's/CI's real Messages
// store is never opened. The same `sqlite3` binary production uses runs the
// queries, so this exercises the genuine quoting behavior rather than a stand-in.
package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// newHermeticChatDB redirects $HOME to a temp dir and builds a minimal chat.db
// there containing exactly one message ("hello secret world"). It returns the
// database path so a test can assert against it directly after a query runs.
func newHermeticChatDB(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	dbPath := filepath.Join(home, "Library", "Messages", "chat.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("creating Messages dir: %v", err)
	}

	// The schema mirrors only the columns baseMessageSelect / the search query
	// reference (message + handle), which is all the read path needs.
	schema := `
CREATE TABLE handle (ROWID INTEGER PRIMARY KEY, id TEXT);
CREATE TABLE message (
  ROWID INTEGER PRIMARY KEY,
  text TEXT,
  attributedBody BLOB,
  date INTEGER,
  handle_id INTEGER,
  is_from_me INTEGER,
  is_read INTEGER
);
INSERT INTO handle (ROWID, id) VALUES (1, '+15551234567');
INSERT INTO message (text, attributedBody, date, handle_id, is_from_me, is_read)
  VALUES ('hello secret world', NULL, 0, 1, 0, 1);
`
	runSQLite(t, dbPath, schema)
	return dbPath
}

// runSQLite executes a SQL script against db using the same trusted sqlite3
// binary production resolves, failing the test on error.
func runSQLite(t *testing.T, db, sql string) string {
	t.Helper()
	bin, err := policy.ResolveBinary("sqlite3")
	if err != nil {
		t.Fatalf("resolving sqlite3: %v", err)
	}
	out, err := exec.Command(bin, db, sql).CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 %q: %v (%s)", sql, err, out)
	}
	return strings.TrimSpace(string(out))
}

// messageRowCount returns how many rows remain in the message table — the
// canary that proves an injected "DROP TABLE" never executed.
func messageRowCount(t *testing.T, db string) string {
	t.Helper()
	return runSQLite(t, db, "SELECT count(*) FROM message;")
}

// TestSQLInjection_SearchMessagesIsSafe drives the real search path against the
// hermetic database with both a legitimate term and a battery of injection
// payloads, asserting (a) the legitimate term matches, (b) no payload matches
// anything (the term is treated as a literal substring, wildcards included), and
// (c) the message table is intact afterward — i.e. no injected statement ran.
func TestSQLInjection_SearchMessagesIsSafe(t *testing.T) {
	db := newHermeticChatDB(t)
	ctx := context.Background()
	cap := registry.Capability{Name: "search_messages"}

	// Baseline: a real substring search returns the one message.
	out, err := runSearchMessages(ctx, cap, map[string]any{"query": "secret"})
	if err != nil {
		t.Fatalf("legitimate search returned error: %v", err)
	}
	if !strings.Contains(out, "secret") {
		t.Errorf("legitimate search should match the seeded message, got: %q", out)
	}

	// None of these should match the seeded text, and none should mutate the DB.
	// The classic injection attempts rely on breaking out of the quoted literal;
	// '%' and '_' verify the match is a literal substring (instr), not a LIKE
	// pattern that would treat them as wildcards matching everything.
	payloads := []string{
		`' OR 1=1; DROP TABLE message;--`,
		`'; DROP TABLE message; --`,
		`' OR '1'='1`,
		`%`,
		`_`,
		`x' UNION SELECT 1,2,3,4,5--`,
	}
	for _, p := range payloads {
		out, err := runSearchMessages(ctx, cap, map[string]any{"query": p})
		if err != nil {
			t.Errorf("payload %q returned an unexpected error: %v", p, err)
			continue
		}
		if !strings.Contains(out, "No messages found") {
			t.Errorf("payload %q should match nothing, got: %q", p, out)
		}
	}

	// The canary: the message table and its single row must still be there.
	if got := messageRowCount(t, db); got != "1" {
		t.Fatalf("message table row count = %q after injection attempts, want 1 (an injected statement may have executed)", got)
	}
}

// TestSQLInjection_NulByteRejected confirms a query containing a NUL byte is
// refused before it ever reaches sqlite3, since a NUL could otherwise truncate
// the assembled statement.
func TestSQLInjection_NulByteRejected(t *testing.T) {
	newHermeticChatDB(t)
	_, err := runSearchMessages(context.Background(), registry.Capability{Name: "search_messages"}, map[string]any{"query": "a\x00b"})
	if err == nil {
		t.Error("a query containing a NUL byte should be rejected, got nil error")
	}
}
