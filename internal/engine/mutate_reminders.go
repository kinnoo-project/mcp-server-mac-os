// mutate_reminders.go implements the mutating Reminders operations —
// add_reminder, modify_reminder, complete_reminder, delete_reminder — as
// reversible staged plans, mirroring mutate_calendar.go.
//
// # Reminders' three due-date shapes
//
// A reminder can have no due date, an all-day due date (`allday due date`, a day
// with no time), or a timed due date (`due date` + a `remind me date` alert).
// Every operation that touches due state therefore carries a "due mode" —
// none / allday / timed (plus "keep" for a modify that leaves the due date
// alone) — so both the forward command and the inverse that restores prior
// state speak the same vocabulary. As in Calendar, prior state is captured by a
// read-only probe at stage time and all model values cross into AppleScript as
// "--"-terminated `on run argv` data.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// createReminderScript makes one reminder and optionally gives it a due date and
// completed flag. argv: list, name, body, dueMode, then due (y,m,d,h,mn), then
// completed("true"/"false"). The date components are always present (zeros when
// dueMode is "none") but only read inside the matching branch. It backs both
// add_reminder's forward and delete_reminder's inverse (re-creating a capture).
const createReminderScript = asDateHelpers + `on run argv
	set listName to item 1 of argv
	set theName to item 2 of argv
	set theBody to item 3 of argv
	set dueMode to item 4 of argv
	set markDone to (item 10 of argv) is "true"
	tell application "Reminders"
		set theList to first list whose name is listName
		tell theList
			set r to make new reminder with properties {name:theName, body:theBody}
		end tell
		if dueMode is "timed" then
			set dd to my _mkdate(item 5 of argv, item 6 of argv, item 7 of argv, item 8 of argv, item 9 of argv)
			set due date of r to dd
			set remind me date of r to dd
		else if dueMode is "allday" then
			set dd to my _mkdate(item 5 of argv, item 6 of argv, item 7 of argv, 0, 0)
			set allday due date of r to dd
		end if
		if markDone then set completed of r to true
	end tell
end run`

// deleteReminderByKeyScript removes a single incomplete reminder matched by list
// + name. argv: list, name. It is add_reminder's inverse; only the first match
// is deleted, so a pre-existing same-named reminder is never touched.
const deleteReminderByKeyScript = `on run argv
	set listName to item 1 of argv
	set theName to item 2 of argv
	tell application "Reminders"
		set theList to first list whose name is listName
		tell theList
			set matches to (every reminder whose name is theName and completed is false)
			if (count of matches) > 0 then delete (item 1 of matches)
		end tell
	end tell
end run`

// deleteReminderByIdScript removes the reminder with the given id. argv: list,
// id. This is delete_reminder's forward command.
const deleteReminderByIdScript = `on run argv
	set listName to item 1 of argv
	set theId to item 2 of argv
	tell application "Reminders"
		set theList to first list whose name is listName
		delete (first reminder of theList whose id is theId)
	end tell
end run`

// modifyReminderScript sets a reminder (found by id) to a target state. It
// always sets the name; it sets body only when setBody is "1"; it touches the
// due date only when dueMode is not "keep". argv: list, id, name,
// setBody("1"/"0"), body, dueMode, then due (y,m,d,h,mn). Serves both forward
// (new values) and inverse (captured values).
const modifyReminderScript = asDateHelpers + `on run argv
	set listName to item 1 of argv
	set theId to item 2 of argv
	set theName to item 3 of argv
	set setBody to (item 4 of argv) is "1"
	set theBody to item 5 of argv
	set dueMode to item 6 of argv
	tell application "Reminders"
		set theList to first list whose name is listName
		set r to first reminder of theList whose id is theId
		set name of r to theName
		if setBody then set body of r to theBody
		if dueMode is "none" then
			set due date of r to missing value
			set allday due date of r to missing value
			set remind me date of r to missing value
		else if dueMode is "allday" then
			set due date of r to missing value
			set allday due date of r to my _mkdate(item 7 of argv, item 8 of argv, item 9 of argv, 0, 0)
		else if dueMode is "timed" then
			set allday due date of r to missing value
			set dd to my _mkdate(item 7 of argv, item 8 of argv, item 9 of argv, item 10 of argv, item 11 of argv)
			set due date of r to dd
			set remind me date of r to dd
		end if
	end tell
end run`

// completeReminderScript sets a reminder's completed flag. argv: list, id,
// value("true"/"false"). Forward sets true; inverse sets false.
const completeReminderScript = `on run argv
	set listName to item 1 of argv
	set theId to item 2 of argv
	set theVal to (item 3 of argv) is "true"
	tell application "Reminders"
		set theList to first list whose name is listName
		set completed of (first reminder of theList whose id is theId) to theVal
	end tell
end run`

// probeReminderScript returns a reminder's captured fields, joined by asFieldSep
// (so a multi-line body survives). argv: list, id. Output: name · body ·
// dueKind("none"/"allday"/"timed") · due("YYYY-MM-DD HH:MM" or "") ·
// completed("yes"/"no").
const probeReminderScript = asDateHelpers + `on run argv
	set listName to item 1 of argv
	set theId to item 2 of argv
	set sep to (character id 31)
	tell application "Reminders"
		set theList to first list whose name is listName
		set r to first reminder of theList whose id is theId
		set dueKind to "none"
		set dueFmt to ""
		if (allday due date of r) is not missing value then
			set dueKind to "allday"
			set dueFmt to my _fmt(allday due date of r)
		else if (due date of r) is not missing value then
			set dueKind to "timed"
			set dueFmt to my _fmt(due date of r)
		end if
		set doneText to "no"
		if completed of r then set doneText to "yes"
		return my _str(name of r) & sep & my _str(body of r) & sep & dueKind & sep & dueFmt & sep & doneText
	end tell
end run`

// reminderDue is a parsed due-date request: a mode plus, for allday/timed, the
// day/time components. mode is one of "none", "allday", "timed", or (modify
// only) "keep".
type reminderDue struct {
	mode  string
	comps ymdhm
}

// args renders the five date-component argv strings; zeros when there is no
// concrete date (none/keep), which the script ignores.
func (d reminderDue) args() []string { return d.comps.args() }

// parseReminderDue interprets the due_date/due_time inputs. noDate is the mode
// to use when due_date is absent: "none" for add/delete (no due) or "keep" for
// modify (leave the existing due alone). due_time without due_date is an error.
func parseReminderDue(op, noDate string, in map[string]any) (reminderDue, error) {
	dueDate, hasDate := getString(in, "due_date")
	dueTime, hasTime := getString(in, "due_time")
	if !hasDate || dueDate == "" {
		if hasTime && dueTime != "" {
			return reminderDue{}, fmt.Errorf("%s: due_time requires due_date", op)
		}
		return reminderDue{mode: noDate}, nil
	}
	y, mo, d, err := parseDate(dueDate)
	if err != nil {
		return reminderDue{}, fmt.Errorf("%s: %w", op, err)
	}
	if hasTime && dueTime != "" {
		h, mi, err := parseClock(dueTime)
		if err != nil {
			return reminderDue{}, fmt.Errorf("%s: due_time: %w", op, err)
		}
		return reminderDue{mode: "timed", comps: ymdhm{y, mo, d, h, mi}}, nil
	}
	return reminderDue{mode: "allday", comps: ymdhm{y, mo, d, 0, 0}}, nil
}

// stageAddReminder stages a reversible reminder creation.
func stageAddReminder(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	list, _ := getString(in, "list")
	title, _ := getString(in, "title")
	if list == "" || title == "" {
		return nil, fmt.Errorf("add_reminder: 'list' and 'title' are required")
	}
	notes, _ := getString(in, "notes")
	due, err := parseReminderDue("add_reminder", "none", in)
	if err != nil {
		return nil, err
	}

	forwardArgs := append([]string{list, title, notes, due.mode}, due.args()...)
	forwardArgs = append(forwardArgs, "false") // a newly created reminder is incomplete
	inverse := osascriptCommand(deleteReminderByKeyScript, list, title)

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Create reminder %q in list %q%s%s.\n\nUndo will delete this reminder.",
			title, list, reminderDuePreview(due), reminderNotesPreview(notes),
		),
		Forward: osascriptCommand(createReminderScript, forwardArgs...),
		Inverse: &inverse,
	}, nil
}

// stageModifyReminder stages a reversible edit, probing prior state so the
// inverse can restore it (including the prior due-date shape).
func stageModifyReminder(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	list, _ := getString(in, "list")
	id, _ := getString(in, "id")
	title, _ := getString(in, "title")
	if list == "" || id == "" || title == "" {
		return nil, fmt.Errorf("modify_reminder: 'list', 'id', and 'title' are required")
	}
	newNotes, setNotes := getString(in, "notes")
	// Absent due_date means "keep" — leave the existing due date untouched.
	due, err := parseReminderDue("modify_reminder", "keep", in)
	if err != nil {
		return nil, err
	}

	prior, err := probeReminder(ctx, list, id)
	if err != nil {
		return nil, err
	}

	// The inverse touches the due date only if the forward did (due.mode !=
	// "keep"); otherwise it leaves it alone, restoring just name/body.
	priorDueMode := "keep"
	if due.mode != "keep" {
		priorDueMode = prior.due.mode
	}
	forward := osascriptCommand(modifyReminderScript,
		modifyReminderArgs(list, id, title, setNotes, newNotes, due.mode, due.comps)...)
	inverse := osascriptCommand(modifyReminderScript,
		modifyReminderArgs(list, id, prior.name, setNotes, prior.body, priorDueMode, prior.due.comps)...)

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Modify reminder %q (id %s) in list %q:\n  new title:  %s%s%s\n\nUndo will restore the previous values.",
			prior.name, id, list, title,
			reminderModifyDueSuffix(due), reminderModifyNotesSuffix(setNotes, newNotes),
		),
		Forward: forward,
		Inverse: &inverse,
	}, nil
}

// stageCompleteReminder stages a reversible completion. It refuses a reminder
// that is already complete: the forward would be a no-op and the inverse (mark
// incomplete) would then wrongly un-complete a reminder this action never
// changed.
func stageCompleteReminder(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	list, _ := getString(in, "list")
	id, _ := getString(in, "id")
	if list == "" || id == "" {
		return nil, fmt.Errorf("complete_reminder: 'list' and 'id' are required")
	}

	prior, err := probeReminder(ctx, list, id)
	if err != nil {
		return nil, err
	}
	if prior.completed {
		return nil, fmt.Errorf("complete_reminder: reminder %q is already complete", prior.name)
	}

	inverse := osascriptCommand(completeReminderScript, list, id, "false")
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Mark reminder %q (id %s) in list %q as complete.\n\nUndo will mark it incomplete again.",
			prior.name, id, list,
		),
		Forward: osascriptCommand(completeReminderScript, list, id, "true"),
		Inverse: &inverse,
	}, nil
}

// stageDeleteReminder stages a reversible deletion: it captures the whole
// reminder (including its due shape and completed flag) so the inverse can
// re-create it faithfully.
func stageDeleteReminder(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	list, _ := getString(in, "list")
	id, _ := getString(in, "id")
	if list == "" || id == "" {
		return nil, fmt.Errorf("delete_reminder: 'list' and 'id' are required")
	}

	prior, err := probeReminder(ctx, list, id)
	if err != nil {
		return nil, err
	}

	inverseArgs := append([]string{list, prior.name, prior.body, prior.due.mode}, prior.due.args()...)
	inverseArgs = append(inverseArgs, boolWord(prior.completed))
	inverse := osascriptCommand(createReminderScript, inverseArgs...)

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Delete reminder %q (id %s) in list %q%s.\n\nUndo will re-create it from these details (note: the re-created reminder will have a new internal id).",
			prior.name, id, list, reminderDuePreview(prior.due),
		),
		Forward: osascriptCommand(deleteReminderByIdScript, list, id),
		Inverse: &inverse,
	}, nil
}

// priorReminder is a reminder's state captured at stage time.
type priorReminder struct {
	name, body string
	due        reminderDue
	completed  bool
}

// probeReminder runs the read-only probe and parses its asFieldSep-joined
// output back into a priorReminder. A bad id surfaces as a non-zero exit.
func probeReminder(ctx context.Context, list, id string) (priorReminder, error) {
	res, err := runOsascript(ctx, probeReminderScript, list, id)
	if err != nil {
		return priorReminder{}, err
	}
	if res.ExitCode != 0 {
		return priorReminder{}, fmt.Errorf("could not read reminder id %q in list %q: %s", id, list, strings.TrimSpace(res.Stderr))
	}
	f := strings.Split(strings.TrimRight(res.Stdout, "\n"), asFieldSep)
	if len(f) != 5 {
		return priorReminder{}, fmt.Errorf("unexpected probe output for reminder id %q (got %d fields)", id, len(f))
	}
	due := reminderDue{mode: f[2]}
	if f[2] == "allday" || f[2] == "timed" {
		comps, err := parseFmtDateTime(f[3])
		if err != nil {
			return priorReminder{}, fmt.Errorf("probe returned an unparseable due date: %w", err)
		}
		due.comps = comps
	}
	return priorReminder{name: f[0], body: f[1], due: due, completed: f[4] == "yes"}, nil
}

// modifyReminderArgs assembles the 11-element argv modifyReminderScript
// consumes, keeping forward and inverse construction identical apart from values.
func modifyReminderArgs(list, id, name string, setBody bool, body, dueMode string, comps ymdhm) []string {
	return append([]string{list, id, name, boolDigit(setBody), body, dueMode}, comps.args()...)
}

// boolWord renders a bool as the "true"/"false" the AppleScript reads.
func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// reminderDuePreview renders a due date for add/delete previews.
func reminderDuePreview(d reminderDue) string {
	switch d.mode {
	case "timed":
		return fmt.Sprintf(", due %s", fmtYMDHM(d.comps))
	case "allday":
		return fmt.Sprintf(", due %04d-%02d-%02d (all day)", d.comps.year, d.comps.month, d.comps.day)
	default:
		return ""
	}
}

// reminderNotesPreview renders the optional notes tail for add previews.
func reminderNotesPreview(notes string) string {
	if notes == "" {
		return ""
	}
	return fmt.Sprintf(" (notes: %s)", notes)
}

// reminderModifyDueSuffix describes a modify's effect on the due date.
func reminderModifyDueSuffix(d reminderDue) string {
	switch d.mode {
	case "timed":
		return fmt.Sprintf("\n  new due:    %s", fmtYMDHM(d.comps))
	case "allday":
		return fmt.Sprintf("\n  new due:    %04d-%02d-%02d (all day)", d.comps.year, d.comps.month, d.comps.day)
	default: // keep
		return ""
	}
}

// reminderModifyNotesSuffix describes a modify's effect on the notes field.
func reminderModifyNotesSuffix(setNotes bool, notes string) string {
	if !setNotes {
		return ""
	}
	return fmt.Sprintf("\n  new notes:  %q", notes)
}
