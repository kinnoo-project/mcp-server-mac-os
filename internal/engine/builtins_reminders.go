// builtins_reminders.go implements the read-only Reminders operation,
// list_reminders. Like the Calendar reads it drives Reminders.app through the
// hardened osascript path and parses tab-delimited stdout in Go. The mutating
// Reminders operations live in mutate_reminders.go.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// Reminders shares Calendar's read caps: a readable default and a hard ceiling.
const (
	defaultReminderLimit = 25
	maxReminderLimit     = 100
)

// listRemindersScript emits one tab-delimited row per reminder:
//
//	id \t list \t name \t due("YYYY-MM-DD HH:MM" or "") \t completed("yes"/"no")
//
// argv (data, after "--"): listFilter includeCompleted("1"/"0") limit. An empty
// listFilter means "all lists". Helper calls inside the `tell` block are `my`-
// prefixed so they resolve to this script's handlers, not Reminders' dictionary.
// A reminder with no due date yields "" for the due field (via _str).
const listRemindersScript = asDateHelpers + `on run argv
	set listFilter to item 1 of argv
	set includeCompleted to (item 2 of argv) is "1"
	set maxN to (item 3 of argv) as integer
	set out to ""
	set n to 0
	tell application "Reminders"
		if listFilter is "" then
			set theLists to every list
		else
			set theLists to (every list whose name is listFilter)
		end if
		repeat with l in theLists
			if n ≥ maxN then exit repeat
			if includeCompleted then
				set rms to every reminder of l
			else
				set rms to (every reminder of l whose completed is false)
			end if
			repeat with r in rms
				if n ≥ maxN then exit repeat
				set dueText to ""
				if (due date of r) is not missing value then set dueText to my _fmt(due date of r)
				set doneText to "no"
				if completed of r then set doneText to "yes"
				set out to out & my _clean(id of r) & tab & my _clean(name of l) & tab & my _clean(name of r) & tab & dueText & tab & doneText & linefeed
				set n to n + 1
			end repeat
		end repeat
	end tell
	return out
end run`

// runListReminders lists reminders, optionally restricted to one list and/or
// including completed ones, capped at limit.
func runListReminders(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	listFilter, _ := getString(in, "list")
	includeCompleted := getBool(in, "include_completed")
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultReminderLimit
	}
	if limit > maxReminderLimit {
		limit = maxReminderLimit
	}

	res, err := runOsascript(ctx, listRemindersScript, listFilter, boolDigit(includeCompleted), itoa(limit))
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", remindersScriptError("list_reminders", res.Stderr)
	}

	rows := asRows(res.Stdout)
	if len(rows) == 0 {
		return "No reminders found.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d reminder(s):\n", len(rows))
	for i, row := range rows {
		f := strings.Split(row, "\t")
		if len(f) != 5 {
			fmt.Fprintf(&b, "%2d. (unparsed) %s\n", i+1, row)
			continue
		}
		id, list, name, due, done := f[0], f[1], f[2], f[3], f[4]
		line := fmt.Sprintf("%2d. [%s] %s", i+1, list, name)
		if due != "" {
			line += " (due " + due + ")"
		}
		if done == "yes" {
			line += " ✓"
		}
		fmt.Fprintf(&b, "%s\n      id: %s\n", line, id)
	}
	return b.String(), nil
}

// boolDigit renders a bool as the "1"/"0" the AppleScript reads as a flag.
func boolDigit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// remindersScriptError mirrors calendarScriptError: it hints at the most likely
// cause (Reminders automation access not granted) rather than surfacing a bare
// AppleScript error.
func remindersScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: Reminders automation failed (%s). If this is the first use, macOS may need you to grant this app access to Reminders in System Settings → Privacy & Security → Automation", op, stderr)
}
