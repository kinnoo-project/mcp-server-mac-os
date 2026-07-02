// builtins_spotlight.go implements spotlight_search: a general-purpose
// Spotlight (mdfind) lookup that answers the everyday "find that file about X"
// question the literal find/grep tools cannot.
//
// find matches on name/extension/type and grep matches on file contents line by
// line; neither understands Spotlight's index, which ranks documents by their
// indexed metadata AND full-text content across the whole home directory in one
// cheap call. spotlight_search exposes that index directly: a free-text query,
// optionally scoped to one directory, returning the matching paths.
//
// Like search_mail this is a builtin rather than a generic-builder capability
// for one security-critical reason: mdfind has NO "--" end-of-options
// terminator (it rejects "--" outright), so the generic builder's structural
// dash-guard cannot protect it. The guard therefore lives here in Go — a
// dash-leading query is refused up front (darwin-execution.md §4), and the
// optional scope directory is resolved to an absolute path before use, which
// makes a leading dash structurally impossible for mdfind to read as a flag.
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

// defaultSpotlightLimit is how many matching paths spotlight_search returns when
// the caller omits "limit". Spotlight can match thousands of items, so the
// default is deliberately small to keep the answer scannable.
const defaultSpotlightLimit = 30

// maxSpotlightLimit caps "limit" regardless of what a caller requests, so a
// pathological request cannot flood the model's context with paths. Builtin
// output bypasses the subprocess truncation budget, so this bound is the only
// thing keeping the response context-safe.
const maxSpotlightLimit = 200

// runSpotlightSearch runs a free-text Spotlight query via mdfind, optionally
// scoped to a single directory, and returns the matching paths (bounded by
// limit).
func runSpotlightSearch(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	query, _ := getString(in, "query")
	// Trim once, up front, and use the trimmed value for everything downstream
	// (the required-check, the dash guard below, and the argv). This is what
	// makes the "first character" guarantee below sound: without it a value like
	// " -e" would slip past the dash guard (its first char is a space) yet reach
	// mdfind with a leading dash once whitespace is disregarded.
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("spotlight_search: 'query' is required")
	}
	// Guard against mdfind option injection. mdfind parses an argument beginning
	// with "-" as one of its OWN options (e.g. "-name", "-onlyin"), not as a
	// search term, and it has no "--" terminator to force the argument to be read
	// as data. So we refuse a dash-leading query outright (darwin-execution.md
	// §4). Only the FIRST character matters: once the (trimmed) argument starts
	// with a non-dash mdfind treats the whole thing as the query, so an interior
	// dash ("foo -bar") is already safe.
	if strings.HasPrefix(query, "-") {
		return "", fmt.Errorf("spotlight_search: 'query' must not begin with '-' (mdfind would parse it as an option, and mdfind has no '--' terminator to prevent that)")
	}

	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultSpotlightLimit
	}
	if limit > maxSpotlightLimit {
		limit = maxSpotlightLimit
	}

	// Assemble mdfind's argv. When a scope directory is supplied we resolve it to
	// an absolute path first: besides giving mdfind an unambiguous root, an
	// absolute path always begins with "/", so a model-supplied value can never
	// reach mdfind as a dash-leading token that "-onlyin" would misread.
	args := make([]string, 0, 3)
	scopeNote := ""
	if dir, ok := getString(in, "dir"); ok && strings.TrimSpace(dir) != "" {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("spotlight_search: resolving scope directory %q: %w", dir, err)
		}
		info, err := os.Stat(absDir)
		if err != nil {
			return "", fmt.Errorf("spotlight_search: scope directory: %w", err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("spotlight_search: scope %q is not a directory", absDir)
		}
		args = append(args, "-onlyin", absDir)
		scopeNote = fmt.Sprintf(" under %s", absDir)
	}
	args = append(args, query)

	mdfindBin, err := policy.ResolveBinary("mdfind")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, mdfindBin, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("spotlight_search: mdfind exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	paths := splitNonEmptyLines(res.Stdout)
	return formatSpotlightResults(query, scopeNote, paths, limit), nil
}

// formatSpotlightResults renders mdfind's matching paths as a compact numbered
// list, capped at limit, with a footer that is honest about how many matches
// were found versus shown. Splitting this out from runSpotlightSearch keeps the
// formatting (including the truncation footer) unit-testable without a live
// mdfind call.
func formatSpotlightResults(query, scopeNote string, paths []string, limit int) string {
	total := len(paths)
	if total == 0 {
		return fmt.Sprintf("No items found matching %q%s.", query, scopeNote)
	}
	shown := paths
	if total > limit {
		shown = paths[:limit]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d item(s) matching %q%s:\n", total, query, scopeNote)
	for i, p := range shown {
		fmt.Fprintf(&b, "%2d. %s\n", i+1, p)
	}
	if total > len(shown) {
		fmt.Fprintf(&b, "(showing the first %d of %d matches; narrow the query or set a scope directory to see more)", len(shown), total)
	}
	return b.String()
}
