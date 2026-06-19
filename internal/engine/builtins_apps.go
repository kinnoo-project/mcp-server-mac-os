// builtins_apps.go implements the read-only Application discovery builtins:
// listing/searching installed applications (via Spotlight's mdfind) and listing
// the applications currently running (via System Events).
//
// These are the "what's on this Mac / what's open right now" reads that the
// mutating launch/focus/quit operations (mutate_apps.go) act on. Like the other
// builtins they compose trusted-binary calls in Go rather than exposing the raw
// utilities, and parse their output into a clean, bounded listing.
package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// Application listing bounds: a readable default and a hard ceiling, mirroring
// the limits the mail/contacts reads impose so a broad listing cannot flood the
// model's context.
const (
	defaultListAppsLimit  = 200
	defaultSearchAppLimit = 50
	maxListAppsLimit      = 500
)

// appBundleQuery is the FIXED Spotlight query that selects application bundles.
// It is a constant — never built from model input — so mdfind (which has no "--"
// terminator) can never be steered by a dash-leading or crafted value. Any
// name filtering happens afterwards in Go against the parsed results.
const appBundleQuery = "kMDItemContentType == 'com.apple.application-bundle'"

// runListApplications lists installed applications, optionally narrowed to those
// whose name contains a caller-supplied substring.
func runListApplications(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	filter, _ := getString(in, "name_contains")
	limit := boundedLimit(in, defaultListAppsLimit)
	return listApps(ctx, filter, limit)
}

// runSearchApplications is the focused counterpart to runListApplications: the
// name substring is required rather than optional.
func runSearchApplications(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	query, _ := getString(in, "query")
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("search_applications: 'query' is required")
	}
	limit := boundedLimit(in, defaultSearchAppLimit)
	return listApps(ctx, query, limit)
}

// boundedLimit reads an optional "limit" parameter, applying the given default
// when absent/invalid and the shared hard ceiling regardless of what is asked.
func boundedLimit(in map[string]any, def int) int {
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = def
	}
	if limit > maxListAppsLimit {
		limit = maxListAppsLimit
	}
	return limit
}

// listApps runs the fixed app-bundle Spotlight query, filters the results by an
// optional case-insensitive name substring in Go, de-duplicates by name, and
// renders a bounded, alphabetically sorted listing. Filtering in Go (rather than
// in the mdfind query) is deliberate: it keeps untrusted text out of mdfind,
// which has no option terminator to neutralise a dash-leading value.
func listApps(ctx context.Context, filter string, limit int) (string, error) {
	mdfindBin, err := policy.ResolveBinary("mdfind")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, mdfindBin, appBundleQuery)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("list_applications: mdfind exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return renderAppList(res.Stdout, filter, limit), nil
}

// renderAppList is the pure formatting half of listApps, split out so the
// de-duplication, filtering, sorting, and truncation can be unit-tested without
// a live mdfind call. It takes mdfind's raw newline-separated paths, an optional
// case-insensitive name filter, and a result cap.
func renderAppList(stdout, filter string, limit int) string {
	filter = strings.ToLower(strings.TrimSpace(filter))

	// Map app name -> path, de-duplicating by name (the same app can be indexed
	// under several paths). Keep the first path seen for each name.
	paths := make(map[string]string)
	for _, p := range splitNonEmptyLines(stdout) {
		name := strings.TrimSuffix(filepath.Base(p), ".app")
		if name == "" {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(name), filter) {
			continue
		}
		if _, seen := paths[name]; !seen {
			paths[name] = p
		}
	}

	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) == 0 {
		if filter != "" {
			return fmt.Sprintf("No installed applications match %q.", filter)
		}
		return "No installed applications found."
	}

	total := len(names)
	truncated := false
	if len(names) > limit {
		names = names[:limit]
		truncated = true
	}

	var b strings.Builder
	if filter != "" {
		fmt.Fprintf(&b, "Found %d application(s) matching %q", total, filter)
	} else {
		fmt.Fprintf(&b, "Found %d installed application(s)", total)
	}
	if truncated {
		fmt.Fprintf(&b, " (showing the first %d)", limit)
	}
	b.WriteString(":\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %s — %s\n", name, paths[name])
	}
	return b.String()
}

// listRunningAppsScript emits one foreground application name per line. It reads
// only the names of processes that are not "background only", i.e. the apps a
// user would recognise as open, skipping daemons and agents.
const listRunningAppsScript = `on run argv
	set out to ""
	tell application "System Events"
		repeat with p in (every process whose background only is false)
			set out to out & (name of p) & linefeed
		end repeat
	end tell
	return out
end run`

// runListRunningApplications lists the foreground applications currently running.
func runListRunningApplications(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	res, err := runOsascript(ctx, listRunningAppsScript)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", appScriptError("list_running_applications", res.Stderr)
	}
	names := asRows(res.Stdout)
	if len(names) == 0 {
		return "No foreground applications appear to be running.", nil
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%d application(s) currently running:\n", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "  %s\n", n)
	}
	return b.String(), nil
}

// appScriptError mirrors phoneScriptError/calendarScriptError: it surfaces the
// most likely cause of a non-zero osascript exit — automation access to System
// Events or the target app not yet granted — with an actionable hint rather than
// a bare AppleScript error string.
func appScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: application control failed (%s). If this is the first use, macOS may need you to grant this app access in System Settings → Privacy & Security → Automation", op, stderr)
}
