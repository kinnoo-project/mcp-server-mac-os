// builtins_calendar.go implements the read-only Calendar operations:
// list_calendars and query_events. Both drive Calendar.app through its
// AppleScript dictionary via the hardened osascript path in applescript.go, then
// parse the script's tab-delimited stdout into human-readable text in Go.
//
// Reads are kept here (as builtins answered in-process) rather than as generic
// argv builders because the work is a fixed AppleScript program plus result
// parsing, not a flag-assembly grammar — the same reason search_mail is a
// builtin. The mutating Calendar operations live in mutate_calendar.go.
//
// Note on Calendar AppleScript: a `whose start date ≥ ...` query over a busy
// calendar can take a few seconds; the result cap below bounds how much we ask
// for and how much we hand back to the model. The first call also triggers a
// one-time macOS automation-permission prompt for controlling Calendar (see
// docs/issues/note-calendar-reminders-tcc-permission.md).
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// defaultEventLimit / maxEventLimit bound query_events the way search_mail's
// limits bound mdls: a default that keeps output readable and a hard ceiling a
// caller cannot exceed.
const (
	defaultEventLimit = 25
	maxEventLimit     = 100
)

// listCalendarsScript returns one calendar name per line. It needs no helpers
// and no arguments, so it is a standalone tell block.
const listCalendarsScript = `on run argv
	set out to ""
	tell application "Calendar"
		repeat with c in calendars
			set out to out & (name of c) & linefeed
		end repeat
	end tell
	return out
end run`

// runListCalendars lists the configured calendars by name.
func runListCalendars(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	res, err := runOsascript(ctx, listCalendarsScript)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", calendarScriptError("list_calendars", res.Stderr)
	}
	names := asRows(res.Stdout)
	if len(names) == 0 {
		return "No calendars found.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Calendars (%d):\n", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "  - %s\n", strings.TrimSpace(n))
	}
	return b.String(), nil
}

// queryEventsScript emits one tab-delimited row per event:
//
//	uid \t calendar \t summary \t "YYYY-MM-DD HH:MM" start \t end \t location
//
// argv (all data, after the "--" terminator): startY startM startD endY endM
// endD limit calendarFilter. An empty calendarFilter means "all calendars".
// Helper calls inside the `tell application "Calendar"` block are prefixed with
// `my` so they resolve to this script's handlers, not to Calendar's dictionary.
const queryEventsScript = asDateHelpers + `on run argv
	set startBoundary to _mkdate(item 1 of argv, item 2 of argv, item 3 of argv, 0, 0)
	set endBoundary to _mkdate(item 4 of argv, item 5 of argv, item 6 of argv, 23, 59) + 59
	set maxN to (item 7 of argv) as integer
	set calFilter to item 8 of argv
	set out to ""
	set n to 0
	tell application "Calendar"
		if calFilter is "" then
			set theCals to every calendar
		else
			set theCals to (every calendar whose name is calFilter)
		end if
		repeat with c in theCals
			if n ≥ maxN then exit repeat
			set evts to (every event of c whose start date ≥ startBoundary and start date ≤ endBoundary)
			repeat with e in evts
				if n ≥ maxN then exit repeat
				set out to out & my _clean(uid of e) & tab & my _clean(name of c) & tab & my _clean(summary of e) & tab & my _fmt(start date of e) & tab & my _fmt(end date of e) & tab & my _clean(my _str(location of e)) & linefeed
				set n to n + 1
			end repeat
		end repeat
	end tell
	return out
end run`

// runQueryEvents lists events in [start_date, end_date] (end inclusive, whole
// day), optionally restricted to one calendar, capped at limit.
func runQueryEvents(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	startStr, _ := getString(in, "start_date")
	sy, sm, sd, err := parseDate(startStr)
	if err != nil {
		return "", fmt.Errorf("query_events: %w", err)
	}
	// end_date defaults to start_date, making a single-day query the common case.
	endStr, ok := getString(in, "end_date")
	if !ok || endStr == "" {
		endStr = startStr
	}
	ey, em, ed, err := parseDate(endStr)
	if err != nil {
		return "", fmt.Errorf("query_events: %w", err)
	}
	if endStr < startStr { // YYYY-MM-DD strings compare chronologically
		return "", fmt.Errorf("query_events: end_date %q is before start_date %q", endStr, startStr)
	}

	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultEventLimit
	}
	if limit > maxEventLimit {
		limit = maxEventLimit
	}
	calFilter, _ := getString(in, "calendar")

	res, err := runOsascript(ctx, queryEventsScript,
		itoa(sy), itoa(sm), itoa(sd), itoa(ey), itoa(em), itoa(ed), itoa(limit), calFilter)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", calendarScriptError("query_events", res.Stderr)
	}

	rows := asRows(res.Stdout)
	if len(rows) == 0 {
		return fmt.Sprintf("No events found between %s and %s.", startStr, endStr), nil
	}
	// Sort chronologically: the start field (index 3) is "YYYY-MM-DD HH:MM",
	// which sorts lexicographically in time order, turning per-calendar results
	// into a single merged agenda.
	sort.SliceStable(rows, func(i, j int) bool {
		return eventStartField(rows[i]) < eventStartField(rows[j])
	})

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d event(s) between %s and %s:\n", len(rows), startStr, endStr)
	for i, row := range rows {
		f := strings.Split(row, "\t")
		// Defensive: a malformed row (wrong field count) is shown raw rather than
		// indexed into, so one odd record can't panic the whole listing.
		if len(f) != 6 {
			fmt.Fprintf(&b, "%2d. (unparsed) %s\n", i+1, row)
			continue
		}
		uid, cal, summary, start, end, loc := f[0], f[1], f[2], f[3], f[4], f[5]
		line := fmt.Sprintf("%2d. [%s] %s — %s to %s", i+1, cal, summary, start, end)
		if loc != "" {
			line += " @ " + loc
		}
		fmt.Fprintf(&b, "%s\n      uid: %s\n", line, uid)
	}
	return b.String(), nil
}

// eventStartField returns the start-date field of a tab-delimited event row, or
// the whole row when it is malformed (so sorting still has a stable key).
func eventStartField(row string) string {
	f := strings.Split(row, "\t")
	if len(f) != 6 {
		return row
	}
	return f[3]
}

// calendarScriptError wraps an osascript failure with a hint about the most
// common cause — automation access to Calendar not yet granted — so the caller
// gets an actionable message instead of a bare AppleScript error number.
func calendarScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: Calendar automation failed (%s). If this is the first use, macOS may need you to grant this app access to Calendar in System Settings → Privacy & Security → Automation", op, stderr)
}
