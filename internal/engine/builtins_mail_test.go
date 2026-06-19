// builtins_mail_test.go tests search_mail's pure parsing logic exhaustively
// (no subprocess, no real mail data), plus a couple of safe real-subprocess
// checks against a query guaranteed not to match anything.
//
// SAFETY: no test here ever searches for or prints real mail content. The
// only real `mdfind` invocation uses an obviously nonsensical query token
// specifically so it returns zero results regardless of what mail exists on
// the machine running the test — confirmed empirically that mdfind exits 0
// with empty output for a query that matches nothing, even when -onlyin
// points at a directory that doesn't exist.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func TestParseMdlsOutput(t *testing.T) {
	raw := "kMDItemDisplayName = \"Alice Example\"\n" +
		"kMDItemSubject     = \"Re: Project update\"\n" +
		"kMDItemContentCreationDate = 2025-07-19 05:13:32 +0000\n"
	subject, sender, date := parseMdlsOutput(raw)
	if subject != "Re: Project update" {
		t.Errorf("subject = %q", subject)
	}
	if sender != "Alice Example" {
		t.Errorf("sender = %q", sender)
	}
	if date != "2025-07-19 05:13:32 +0000" {
		t.Errorf("date = %q", date)
	}
}

// TestParseMdlsOutput_MissingAttribute confirms a "(null)" attribute (mdls's
// own rendering for an absent value) is passed through as-is, not silently
// blanked — a genuinely missing subject should read as "(null)", not "".
func TestParseMdlsOutput_MissingAttribute(t *testing.T) {
	raw := "kMDItemDisplayName = \"Mail\"\nkMDItemSubject = (null)\n"
	subject, sender, _ := parseMdlsOutput(raw)
	if subject != "(null)" {
		t.Errorf("subject = %q, want \"(null)\"", subject)
	}
	if sender != "Mail" {
		t.Errorf("sender = %q", sender)
	}
}

// TestParseMdlsOutput_OrderIndependent confirms parsing doesn't assume a
// fixed attribute order — confirmed empirically that mdls does not return
// attributes in the order they were requested.
func TestParseMdlsOutput_OrderIndependent(t *testing.T) {
	raw := "kMDItemSubject = \"X\"\nkMDItemContentCreationDate = 2024-01-01 00:00:00 +0000\nkMDItemDisplayName = \"Y\"\n"
	subject, sender, date := parseMdlsOutput(raw)
	if subject != "X" || sender != "Y" || date != "2024-01-01 00:00:00 +0000" {
		t.Errorf("got subject=%q sender=%q date=%q", subject, sender, date)
	}
}

func TestSplitNonEmptyLines(t *testing.T) {
	if got := splitNonEmptyLines(""); got != nil {
		t.Errorf("empty input should yield nil, got %v", got)
	}
	if got := splitNonEmptyLines("  \n  "); got != nil {
		t.Errorf("whitespace-only input should yield nil, got %v", got)
	}
	got := splitNonEmptyLines("/a/one.emlx\n/a/two.emlx\n")
	want := []string{"/a/one.emlx", "/a/two.emlx"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRunSearchMail_RequiresQuery(t *testing.T) {
	if _, err := runSearchMail(context.Background(), searchMailCapability(t), map[string]any{}); err == nil {
		t.Fatal("expected an error when 'query' is omitted")
	}
}

// TestRunSearchMail_NoMatchesReportsCleanly drives a real mdfind call with a
// query engineered to match nothing, confirming the no-results path renders
// a clean message rather than an error or empty output. See the SAFETY note
// at the top of this file.
func TestRunSearchMail_NoMatchesReportsCleanly(t *testing.T) {
	out, err := runSearchMail(context.Background(), searchMailCapability(t), map[string]any{
		"query": "zzz-search-mail-test-token-guaranteed-no-match-9f3e7c1a",
	})
	if err != nil {
		t.Fatalf("runSearchMail: %v", err)
	}
	if !strings.Contains(out, "No mail found") {
		t.Errorf("expected a clean no-results message, got %q", out)
	}
}

func searchMailCapability(t *testing.T) registry.Capability {
	t.Helper()
	return lookupCapability(t, "search_mail")
}
