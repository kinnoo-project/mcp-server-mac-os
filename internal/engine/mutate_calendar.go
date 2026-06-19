// mutate_calendar.go implements the mutating Calendar operations — add_event,
// modify_event, delete_event — as reversible staged plans.
//
// # How undo is made possible for an app we don't own
//
// Unlike `defaults` (where the inverse is "write the prior value back"), we
// can't snapshot Calendar's internal store. Instead each operation's inverse is
// expressed in the SAME AppleScript vocabulary as its forward, with prior state
// captured at stage time by a read-only probe:
//
//   - add_event:    forward creates the event; inverse deletes it again. Because
//     the new event's identity isn't known until it exists, the inverse matches
//     it by natural key (calendar + summary + exact start date) and removes a
//     single match — see the fidelity caveat in
//     docs/issues/note-calendar-reminders-undo-fidelity.md.
//   - modify_event: a probe captures the event's current summary/start/end/
//     location/notes; forward writes the new values, inverse writes the captured
//     ones back. Both use the one modifyEventScript, differing only in argv.
//   - delete_event: a probe captures the full event; forward deletes it (by
//     uid), inverse re-creates it from the capture (reusing addEventScript).
//
// All model-supplied values cross into AppleScript as `on run argv` data through
// osascriptCommand's hardened, "--"-terminated path; the script bodies are fixed
// constants. Time values are passed as integer components (never locale-
// dependent date strings) and rebuilt with _mkdate.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// addEventScript creates one event. argv: calendar, summary, location, notes,
// then start (y,m,d,h,mn) then end (y,m,d,h,mn). It is reused verbatim as
// delete_event's inverse (re-creating a captured event).
const addEventScript = asDateHelpers + `on run argv
	set calName to item 1 of argv
	set theSummary to item 2 of argv
	set theLocation to item 3 of argv
	set theNotes to item 4 of argv
	set startDate to _mkdate(item 5 of argv, item 6 of argv, item 7 of argv, item 8 of argv, item 9 of argv)
	set endDate to _mkdate(item 10 of argv, item 11 of argv, item 12 of argv, item 13 of argv, item 14 of argv)
	tell application "Calendar"
		set theCal to first calendar whose name is calName
		tell theCal
			make new event with properties {summary:theSummary, start date:startDate, end date:endDate, location:theLocation, description:theNotes}
		end tell
	end tell
end run`

// deleteEventByKeyScript removes a single event matched by calendar + summary +
// exact start date. argv: calendar, summary, then start (y,m,d,h,mn). It is
// add_event's inverse: it targets the event add_event just created. Only the
// FIRST match is deleted, so pre-existing identical events are never touched.
const deleteEventByKeyScript = asDateHelpers + `on run argv
	set calName to item 1 of argv
	set theSummary to item 2 of argv
	set startDate to _mkdate(item 3 of argv, item 4 of argv, item 5 of argv, item 6 of argv, item 7 of argv)
	tell application "Calendar"
		set theCal to first calendar whose name is calName
		tell theCal
			set matches to (every event whose summary is theSummary and start date is startDate)
			if (count of matches) > 0 then delete (item 1 of matches)
		end tell
	end tell
end run`

// deleteEventByUidScript removes the event with the given uid from a calendar.
// argv: calendar, uid. This is delete_event's forward command.
const deleteEventByUidScript = `on run argv
	set calName to item 1 of argv
	set theUid to item 2 of argv
	tell application "Calendar"
		set theCal to first calendar whose name is calName
		delete (first event of theCal whose uid is theUid)
	end tell
end run`

// modifyEventScript sets an event (found by uid) to a target state. It always
// sets summary/start/end; it sets location and/or notes only when the matching
// "set" flag is "1". argv: calendar, uid, summary, setLocation("1"/"0"),
// location, setNotes("1"/"0"), notes, then start (y,m,d,h,mn), then end. The
// same script serves both forward (new values) and inverse (captured values).
const modifyEventScript = asDateHelpers + `on run argv
	set calName to item 1 of argv
	set theUid to item 2 of argv
	set theSummary to item 3 of argv
	set setLoc to (item 4 of argv) is "1"
	set theLocation to item 5 of argv
	set setNotes to (item 6 of argv) is "1"
	set theNotes to item 7 of argv
	set startDate to _mkdate(item 8 of argv, item 9 of argv, item 10 of argv, item 11 of argv, item 12 of argv)
	set endDate to _mkdate(item 13 of argv, item 14 of argv, item 15 of argv, item 16 of argv, item 17 of argv)
	tell application "Calendar"
		set theCal to first calendar whose name is calName
		set theEvent to first event of theCal whose uid is theUid
		set summary of theEvent to theSummary
		set start date of theEvent to startDate
		set end date of theEvent to endDate
		if setLoc then set location of theEvent to theLocation
		if setNotes then set description of theEvent to theNotes
	end tell
end run`

// probeEventScript returns one event's captured fields, joined by asFieldSep so
// a multi-line notes body survives intact. argv: calendar, uid. Output:
// summary · start("YYYY-MM-DD HH:MM") · end · location · notes.
const probeEventScript = asDateHelpers + `on run argv
	set calName to item 1 of argv
	set theUid to item 2 of argv
	set sep to (character id 31)
	tell application "Calendar"
		set theCal to first calendar whose name is calName
		set e to first event of theCal whose uid is theUid
		return my _str(summary of e) & sep & my _fmt(start date of e) & sep & my _fmt(end date of e) & sep & my _str(location of e) & sep & my _str(description of e)
	end tell
end run`

// eventTimes holds an add/modify request's validated day and start/end times.
// In this v1 an event is a single-day span, so both share year/month/day.
type eventTimes struct {
	day        ymdhm // start, with hours/minutes = start_time
	endClockHM ymdhm // end, sharing the same year/month/day, hours/minutes = end_time
}

// parseEventTimes validates the date/start_time/end_time trio common to
// add_event and modify_event and returns the packed components. It enforces a
// same-day end strictly later than start, refusing zero-length or inverted
// spans before any plan is staged.
func parseEventTimes(op string, in map[string]any) (eventTimes, error) {
	dateStr, _ := getString(in, "date")
	startStr, _ := getString(in, "start_time")
	endStr, _ := getString(in, "end_time")

	y, mo, d, err := parseDate(dateStr)
	if err != nil {
		return eventTimes{}, fmt.Errorf("%s: %w", op, err)
	}
	sh, smin, err := parseClock(startStr)
	if err != nil {
		return eventTimes{}, fmt.Errorf("%s: start_time: %w", op, err)
	}
	eh, emin, err := parseClock(endStr)
	if err != nil {
		return eventTimes{}, fmt.Errorf("%s: end_time: %w", op, err)
	}
	if eh*60+emin <= sh*60+smin {
		return eventTimes{}, fmt.Errorf("%s: end_time %s must be later than start_time %s", op, endStr, startStr)
	}
	return eventTimes{
		day:        ymdhm{y, mo, d, sh, smin},
		endClockHM: ymdhm{y, mo, d, eh, emin},
	}, nil
}

// stageAddEvent stages a reversible event creation.
func stageAddEvent(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	calendar, _ := getString(in, "calendar")
	title, _ := getString(in, "title")
	if calendar == "" || title == "" {
		return nil, fmt.Errorf("add_event: 'calendar' and 'title' are required")
	}
	t, err := parseEventTimes("add_event", in)
	if err != nil {
		return nil, err
	}
	location, _ := getString(in, "location")
	notes, _ := getString(in, "notes")

	// Forward: create. Inverse: delete the just-created event by its natural key
	// (calendar + summary + start date), built entirely from values known now.
	forwardArgs := append([]string{calendar, title, location, notes}, t.day.args()...)
	forwardArgs = append(forwardArgs, t.endClockHM.args()...)

	inverseArgs := append([]string{calendar, title}, t.day.args()...)
	inverse := osascriptCommand(deleteEventByKeyScript, inverseArgs...)

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Create event %q in calendar %q on %04d-%02d-%02d from %02d:%02d to %02d:%02d%s.\n\nUndo will delete this event.",
			title, calendar, t.day.year, t.day.month, t.day.day, t.day.hours, t.day.minutes,
			t.endClockHM.hours, t.endClockHM.minutes, locationNotesSuffix(location, notes),
		),
		Forward: osascriptCommand(addEventScript, forwardArgs...),
		Inverse: &inverse,
	}, nil
}

// stageModifyEvent stages a reversible edit of an existing event. It probes the
// event's current state first so the inverse can restore it exactly.
func stageModifyEvent(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	calendar, _ := getString(in, "calendar")
	uid, _ := getString(in, "uid")
	title, _ := getString(in, "title")
	if calendar == "" || uid == "" || title == "" {
		return nil, fmt.Errorf("modify_event: 'calendar', 'uid', and 'title' are required")
	}
	t, err := parseEventTimes("modify_event", in)
	if err != nil {
		return nil, err
	}
	newLocation, setLocation := getString(in, "location")
	newNotes, setNotes := getString(in, "notes")

	prior, err := probeEvent(ctx, calendar, uid)
	if err != nil {
		return nil, err
	}

	// Forward sets the new values (location/notes only when supplied). The
	// inverse sets the captured prior values back — and crucially restores
	// location/notes ONLY if the forward touched them, so an unmodified field is
	// never rewritten.
	forward := osascriptCommand(modifyEventScript,
		modifyEventArgs(calendar, uid, title, setLocation, newLocation, setNotes, newNotes, t.day, t.endClockHM)...)
	inverse := osascriptCommand(modifyEventScript,
		modifyEventArgs(calendar, uid, prior.summary, setLocation, prior.location, setNotes, prior.notes, prior.start, prior.end)...)

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Modify event %q (uid %s) in calendar %q:\n  new title:  %s\n  new time:   %04d-%02d-%02d %02d:%02d to %02d:%02d%s\n\nCurrently: %q, %s to %s.\nUndo will restore the previous values.",
			prior.summary, uid, calendar,
			title, t.day.year, t.day.month, t.day.day, t.day.hours, t.day.minutes, t.endClockHM.hours, t.endClockHM.minutes,
			modifyFieldSuffix(setLocation, newLocation, setNotes, newNotes),
			prior.summary, fmtYMDHM(prior.start), fmtYMDHM(prior.end),
		),
		Forward: forward,
		Inverse: &inverse,
	}, nil
}

// stageDeleteEvent stages a reversible deletion: it captures the whole event up
// front so the inverse can re-create it.
func stageDeleteEvent(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	calendar, _ := getString(in, "calendar")
	uid, _ := getString(in, "uid")
	if calendar == "" || uid == "" {
		return nil, fmt.Errorf("delete_event: 'calendar' and 'uid' are required")
	}

	prior, err := probeEvent(ctx, calendar, uid)
	if err != nil {
		return nil, err
	}

	// Inverse re-creates the captured event via the same script add_event uses.
	inverseArgs := append([]string{calendar, prior.summary, prior.location, prior.notes}, prior.start.args()...)
	inverseArgs = append(inverseArgs, prior.end.args()...)
	inverse := osascriptCommand(addEventScript, inverseArgs...)

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Delete event %q (uid %s) in calendar %q, scheduled %s to %s%s.\n\nUndo will re-create it from these details (note: the re-created event will have a new internal uid).",
			prior.summary, uid, calendar, fmtYMDHM(prior.start), fmtYMDHM(prior.end),
			locationNotesSuffix(prior.location, prior.notes),
		),
		Forward: osascriptCommand(deleteEventByUidScript, calendar, uid),
		Inverse: &inverse,
	}, nil
}

// priorEvent is an event's state captured at stage time, used to build an
// inverse command.
type priorEvent struct {
	summary, location, notes string
	start, end               ymdhm
}

// probeEvent runs the read-only probe and parses its asFieldSep-joined output.
// A bad uid (no such event) surfaces as a non-zero osascript exit, which becomes
// a clear staging error rather than a silently empty capture.
func probeEvent(ctx context.Context, calendar, uid string) (priorEvent, error) {
	res, err := runOsascript(ctx, probeEventScript, calendar, uid)
	if err != nil {
		return priorEvent{}, err
	}
	if res.ExitCode != 0 {
		return priorEvent{}, fmt.Errorf("could not read event uid %q in calendar %q: %s", uid, calendar, strings.TrimSpace(res.Stderr))
	}
	fields := strings.Split(strings.TrimRight(res.Stdout, "\n"), asFieldSep)
	if len(fields) != 5 {
		return priorEvent{}, fmt.Errorf("unexpected probe output for event uid %q (got %d fields)", uid, len(fields))
	}
	start, err := parseFmtDateTime(fields[1])
	if err != nil {
		return priorEvent{}, fmt.Errorf("probe returned an unparseable start time: %w", err)
	}
	end, err := parseFmtDateTime(fields[2])
	if err != nil {
		return priorEvent{}, fmt.Errorf("probe returned an unparseable end time: %w", err)
	}
	return priorEvent{summary: fields[0], location: fields[3], notes: fields[4], start: start, end: end}, nil
}

// modifyEventArgs assembles the 17-element argv modifyEventScript consumes,
// keeping the forward and inverse construction identical apart from the values.
func modifyEventArgs(calendar, uid, summary string, setLoc bool, location string, setNotes bool, notes string, start, end ymdhm) []string {
	args := []string{calendar, uid, summary, boolDigit(setLoc), location, boolDigit(setNotes), notes}
	args = append(args, start.args()...)
	return append(args, end.args()...)
}

// locationNotesSuffix renders the optional " at <loc> (notes: <n>)" tail shown
// in add/delete previews, omitting whichever fields are empty.
func locationNotesSuffix(location, notes string) string {
	var parts []string
	if location != "" {
		parts = append(parts, "at "+location)
	}
	if notes != "" {
		parts = append(parts, "notes: "+notes)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// modifyFieldSuffix describes which optional fields modify_event will change,
// so the preview makes the location/notes effect explicit.
func modifyFieldSuffix(setLoc bool, location string, setNotes bool, notes string) string {
	var parts []string
	if setLoc {
		parts = append(parts, fmt.Sprintf("location → %q", location))
	}
	if setNotes {
		parts = append(parts, fmt.Sprintf("notes → %q", notes))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n  also set:   " + strings.Join(parts, ", ")
}

// fmtYMDHM renders captured components back to the "YYYY-MM-DD HH:MM" shown in
// previews.
func fmtYMDHM(t ymdhm) string {
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d", t.year, t.month, t.day, t.hours, t.minutes)
}
