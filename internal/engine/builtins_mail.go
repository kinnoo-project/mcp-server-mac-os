// builtins_mail.go implements search_mail: a builtin (like largest_files)
// that composes two trusted-binary calls in Go rather than exposing either
// directly, because neither one alone answers the question.
//
// `mdfind` (Spotlight) finds matching mail messages, but only returns file
// paths — not subjects, senders, or dates. `mdls` extracts those per-path.
// Chaining them is exactly the kind of multi-step composition the no-shell
// axiom forbids as a literal pipe; doing it here, in Go, with each step run
// via the same trusted-binary + explicit-argv discipline as every other
// capability, is the safe equivalent (the same reasoning largest_files
// already established for du | sort | head).
//
// search_mail deliberately uses Spotlight rather than Mail.app's own
// AppleScript search — see docs/issues/note-applescript-mail-search-deferred.md
// for the precision tradeoff and why an AppleScript-backed alternative is a
// documented future option, not built now.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// defaultSearchMailLimit is how many results search_mail returns when the
// caller omits "limit".
const defaultSearchMailLimit = 20

// maxSearchMailLimit caps "limit" regardless of what a caller requests, so a
// pathological request can't flood the model with hundreds of mdls calls.
const maxSearchMailLimit = 50

// runSearchMail finds mail messages matching query via Spotlight, then
// extracts subject/sender/date for each match (bounded by limit) via mdls.
func runSearchMail(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	query, _ := getString(in, "query")
	if query == "" {
		return "", fmt.Errorf("search_mail: 'query' is required")
	}
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultSearchMailLimit
	}
	if limit > maxSearchMailLimit {
		limit = maxSearchMailLimit
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("search_mail: %w", err)
	}
	mailDir := filepath.Join(home, "Library", "Mail")

	mdfindBin, err := policy.ResolveBinary("mdfind")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, mdfindBin, "-onlyin", mailDir, query)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("search_mail: mdfind exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	paths := splitNonEmptyLines(res.Stdout)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	if len(paths) == 0 {
		return fmt.Sprintf("No mail found matching %q.", query), nil
	}

	mdlsBin, err := policy.ResolveBinary("mdls")
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d message(s) matching %q:\n", len(paths), query)
	for i, p := range paths {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		meta, mdlsErr := runCommand(ctx, mdlsBin, "-name", "kMDItemSubject", "-name", "kMDItemDisplayName", "-name", "kMDItemContentCreationDate", p)
		if mdlsErr != nil || meta.ExitCode != 0 {
			// A single unreadable result (e.g. the index entry is stale and the
			// file has since moved) shouldn't sink the whole search.
			fmt.Fprintf(&b, "%2d. (metadata unavailable) %s\n", i+1, p)
			continue
		}
		subject, sender, date := parseMdlsOutput(meta.Stdout)
		fmt.Fprintf(&b, "%2d. [%s] %s — %s\n", i+1, date, subject, sender)
	}
	return b.String(), nil
}

// splitNonEmptyLines splits mdfind's newline-separated stdout into paths,
// dropping the trailing empty line a final "\n" would otherwise produce.
func splitNonEmptyLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// parseMdlsOutput extracts subject/sender/date from mdls's "-name" output
// format (one "kMDItemX = value" line per requested attribute, in no
// guaranteed order). A missing attribute renders as "(null)"; this is
// returned as-is rather than papered over, so a genuinely absent subject
// reads as "(null)" instead of a misleadingly blank string.
func parseMdlsOutput(raw string) (subject, sender, date string) {
	fields := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"`)
		fields[key] = val
	}
	return fields["kMDItemSubject"], fields["kMDItemDisplayName"], fields["kMDItemContentCreationDate"]
}
