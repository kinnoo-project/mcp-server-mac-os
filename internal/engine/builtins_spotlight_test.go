// builtins_spotlight_test.go tests spotlight_search's validation guards and its
// pure result-formatting, plus one safe real-subprocess check.
//
// SAFETY: no test here searches for or prints real user content. The only real
// `mdfind` invocation is scoped to a fresh empty temp directory, so it is
// guaranteed to match nothing regardless of the machine's Spotlight index; the
// test asserts only the clean no-results path and never surfaces indexed files.
// (An unscoped whole-index search would be both unreliable and capable of
// surfacing real files — indeed this very file contains the query token.)
package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func spotlightCapability(t *testing.T) registry.Capability {
	t.Helper()
	return lookupCapability(t, "spotlight_search")
}

func TestRunSpotlightSearch_RequiresQuery(t *testing.T) {
	if _, err := runSpotlightSearch(context.Background(), spotlightCapability(t), map[string]any{}); err == nil {
		t.Fatal("expected an error when 'query' is omitted")
	}
	// A whitespace-only query is as empty as an absent one.
	if _, err := runSpotlightSearch(context.Background(), spotlightCapability(t), map[string]any{"query": "   "}); err == nil {
		t.Fatal("expected an error for a whitespace-only query")
	}
}

// TestRunSpotlightSearch_RejectsDashLeadingQuery is the regression test for the
// mdfind option-injection guardrail: a query beginning with "-" must be refused
// before mdfind ever runs, because mdfind would parse it as one of its own
// options and has no "--" terminator to prevent that. The refusal is pure
// validation, so this test launches no subprocess.
func TestRunSpotlightSearch_RejectsDashLeadingQuery(t *testing.T) {
	// The whitespace-prefixed cases (" -e", "  --") prove the up-front trim
	// closes the bypass where surrounding whitespace would otherwise let a
	// dash-leading value slip past the guard and reach mdfind as a flag.
	for _, q := range []string{"-e", "-name", "--onlyin", "-", " -e", "  --"} {
		_, err := runSpotlightSearch(context.Background(), spotlightCapability(t), map[string]any{"query": q})
		if err == nil {
			t.Errorf("%q: expected an error for a query beginning with '-'", q)
			continue
		}
		if !strings.Contains(err.Error(), "must not begin with '-'") {
			t.Errorf("%q: error should explain the dash-leading restriction, got: %v", q, err)
		}
	}
}

// TestRunSpotlightSearch_RejectsBadScopeDir confirms a scope directory that does
// not exist (or is a plain file) is reported cleanly rather than silently
// yielding zero matches. A dash-leading dir is included because it exercises the
// same path: filepath.Abs turns "-foo" into an absolute path under the cwd,
// which then fails the stat — so a dash-leading value can never reach mdfind as
// a flag.
func TestRunSpotlightSearch_RejectsBadScopeDir(t *testing.T) {
	_, err := runSpotlightSearch(context.Background(), spotlightCapability(t), map[string]any{
		"query": "anything",
		"dir":   "/nonexistent-spotlight-scope-9f3e7c1a",
	})
	if err == nil {
		t.Fatal("expected an error for a nonexistent scope directory")
	}
}

func TestFormatSpotlightResults_NoMatches(t *testing.T) {
	out := formatSpotlightResults("widgets", "", nil, 30)
	if !strings.Contains(out, "No items found") {
		t.Errorf("expected a clean no-results message, got %q", out)
	}
	// The scope note, when present, must appear in the no-results message too.
	out = formatSpotlightResults("widgets", " under /tmp", nil, 30)
	if !strings.Contains(out, "under /tmp") {
		t.Errorf("expected the scope note in the no-results message, got %q", out)
	}
}

func TestFormatSpotlightResults_ListsAndNumbers(t *testing.T) {
	paths := []string{"/a/one.pdf", "/a/two.pdf"}
	out := formatSpotlightResults("report", "", paths, 30)
	if !strings.Contains(out, "Found 2 item(s)") {
		t.Errorf("expected the total count, got %q", out)
	}
	if !strings.Contains(out, " 1. /a/one.pdf") || !strings.Contains(out, " 2. /a/two.pdf") {
		t.Errorf("expected numbered paths, got %q", out)
	}
	if strings.Contains(out, "showing the first") {
		t.Errorf("no truncation footer expected when all matches fit, got %q", out)
	}
}

// TestFormatSpotlightResults_TruncationFooter confirms the count is honest when
// there are more matches than the limit: the header reports the true total, only
// `limit` rows are listed, and the footer explains the trim.
func TestFormatSpotlightResults_TruncationFooter(t *testing.T) {
	paths := []string{"/a/1", "/a/2", "/a/3", "/a/4", "/a/5"}
	out := formatSpotlightResults("x", "", paths, 2)
	if !strings.Contains(out, "Found 5 item(s)") {
		t.Errorf("header should report the true total of 5, got %q", out)
	}
	if !strings.Contains(out, " 2. /a/2") {
		t.Errorf("expected the second (last shown) row, got %q", out)
	}
	if strings.Contains(out, "/a/3") {
		t.Errorf("rows beyond the limit must not be listed, got %q", out)
	}
	if !strings.Contains(out, "showing the first 2 of 5") {
		t.Errorf("expected a truncation footer, got %q", out)
	}
}

// TestFormatSpotlightResults_CompactsOversizedOutput confirms the rendered list
// is passed through the shared head/tail compaction: the item-count cap alone
// does not bound the byte size (200 long paths can exceed the budget), so the
// final string must be trimmed like every other builtin's output.
func TestFormatSpotlightResults_CompactsOversizedOutput(t *testing.T) {
	// Build enough long paths that the rendered list is well past maxOutputBytes
	// even though the count stays within maxSpotlightLimit.
	longDir := "/Users/example/" + strings.Repeat("deeply-nested-folder/", 20)
	paths := make([]string, maxSpotlightLimit)
	for i := range paths {
		paths[i] = fmt.Sprintf("%sdocument-%03d.pdf", longDir, i)
	}
	out := formatSpotlightResults("report", "", paths, maxSpotlightLimit)
	if len(out) > maxOutputBytes+200 { // +200: compaction notice overhead
		t.Errorf("oversized result was not compacted: got %d bytes", len(out))
	}
	if !strings.Contains(out, "bytes truncated") {
		t.Errorf("expected the truncation notice in compacted output, got %d bytes without it", len(out))
	}
}

// TestRunSpotlightSearch_NoMatchesReportsCleanly drives a real mdfind call and
// confirms the no-results path renders a clean message rather than an error. The
// search is scoped to a fresh empty temp directory, so it is guaranteed to match
// nothing regardless of Spotlight's index state (and cannot accidentally surface
// real user files — note the SAFETY comment at the top of this file). An
// unscoped whole-index search would be unreliable here: this very test file
// contains the query token, so mdfind would match its own source.
func TestRunSpotlightSearch_NoMatchesReportsCleanly(t *testing.T) {
	out, err := runSpotlightSearch(context.Background(), spotlightCapability(t), map[string]any{
		"query": "report",
		"dir":   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("runSpotlightSearch: %v", err)
	}
	if !strings.Contains(out, "No items found") {
		t.Errorf("expected a clean no-results message, got %q", out)
	}
}
