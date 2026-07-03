// applescript.go is the shared, hardened seam for every capability that drives a
// macOS application through its AppleScript dictionary via `osascript`
// (Mail, Calendar, Reminders, ...).
//
// # The one enforced "data, never code" path
//
// osascript is a full scripting language, so the same discipline the no-shell
// axiom demands for argv applies doubly here: a model-supplied value must reach
// the script as DATA bound to `on run argv`, never as part of the script source.
// Two concrete hazards are handled in exactly one place so no individual
// capability can forget either (see CLAUDE.md §4 axiom 2):
//
//  1. The script body is always a FIXED, reviewed Go constant. Values are passed
//     positionally, never concatenated into the source.
//  2. A "--" end-of-options terminator is ALWAYS inserted between the script and
//     the first data argument. Without it, a value beginning with "-" (e.g. a
//     subject of "-e") would be parsed by osascript as one of its OWN options and
//     the following argument executed as AppleScript — option injection. osascript
//     consumes the "--" itself and does not pass it into argv, so the script's
//     item indices are unaffected.
//
// osascriptCommand builds the staged Command for mutating capabilities;
// runOsascript runs a read-only script directly for read builtins. Both go
// through the same terminator-inserting assembly, so the hardening is structural
// rather than per-call-site.
package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mcp-server-mac-os/internal/policy"
)

// osascriptArgv assembles the tokenized argument vector both osascript paths
// share: the fixed script under "-e", then the "--" end-of-options terminator,
// then every model-supplied data value. Extracting it into one function is what
// makes the "-- terminator is always present" guarantee STRUCTURAL rather than
// duplicated at two call sites — the mutating path (osascriptCommand) and the
// read path (runOsascript) cannot drift apart, and the hardening is unit-tested
// in one place (see applescript_test.go / injection_sweep_test.go).
func osascriptArgv(script string, args ...string) []string {
	argv := make([]string, 0, len(args)+3)
	argv = append(argv, "-e", script, "--")
	return append(argv, args...)
}

// osascriptCommand assembles a fully-tokenized osascript invocation that runs a
// fixed script with the given data arguments bound to its `on run argv`. The
// "--" terminator is inserted unconditionally (see the file header).
func osascriptCommand(script string, args ...string) Command {
	return Command{Binary: "osascript", Args: osascriptArgv(script, args...)}
}

// runOsascript runs a read-only osascript script in-process for a read builtin,
// applying the same fixed-script + "--"-terminator hardening as osascriptCommand
// (both build their argv through osascriptArgv). It returns the raw runResult so
// the caller can inspect the exit code/stderr (an AppleScript error, e.g. a
// denied automation permission, surfaces as a non-zero exit with an explanatory
// stderr) before parsing stdout.
func runOsascript(ctx context.Context, script string, args ...string) (*runResult, error) {
	bin, err := policy.ResolveBinary("osascript")
	if err != nil {
		return nil, err
	}
	return runCommand(ctx, bin, osascriptArgv(script, args...)...)
}

// asDateHelpers is a reusable block of top-level AppleScript handlers that every
// Calendar/Reminders script prepends. Keeping them here, defined once, means the
// per-operation scripts stay small and read as intent.
//
//   - _mkdate builds a `date` from integer components WITHOUT locale ambiguity
//     (no string parsing). It sets day to 1 first so assigning a new month can
//     never overflow a shorter month (e.g. moving from the 31st into February).
//   - _fmt renders a `date` as the stable "YYYY-MM-DD HH:MM" the Go side parses,
//     regardless of the machine's locale/date-format preferences.
//   - _str coerces a possibly-`missing value` property (e.g. an event with no
//     location) to an empty string instead of erroring on the coercion.
//   - _clean flattens any tab/return/linefeed inside a text field to single
//     spaces, so a summary or location containing a newline cannot corrupt the
//     tab-delimited, one-row-per-line output contract the Go parser relies on.
const asDateHelpers = `on _mkdate(y, m, d, h, mn)
	set theDate to current date
	set day of theDate to 1
	set year of theDate to (y as integer)
	set month of theDate to (m as integer)
	set day of theDate to (d as integer)
	set hours of theDate to (h as integer)
	set minutes of theDate to (mn as integer)
	set seconds of theDate to 0
	return theDate
end _mkdate

on _fmt(theDate)
	return _pad(year of theDate, 4) & "-" & _pad((month of theDate) as integer, 2) & "-" & _pad(day of theDate, 2) & " " & _pad(hours of theDate, 2) & ":" & _pad(minutes of theDate, 2)
end _fmt

on _pad(n, w)
	set s to (n as integer) as string
	repeat while (length of s) < w
		set s to "0" & s
	end repeat
	return s
end _pad

on _str(v)
	if v is missing value then return ""
	return v as string
end _str

on _clean(v)
	set s to v as string
	set savedTID to AppleScript's text item delimiters
	set AppleScript's text item delimiters to {tab, return, linefeed}
	set parts to text items of s
	set AppleScript's text item delimiters to " "
	set s to parts as string
	set AppleScript's text item delimiters to savedTID
	return s
end _clean

`

// parseDate validates a "YYYY-MM-DD" calendar date and returns its components.
// time.Parse enforces real calendar validity (it rejects, for example,
// 2025-02-30 or a 13th month), so a malformed or impossible date is refused
// here rather than silently mangled by AppleScript.
func parseDate(s string) (year, month, day int, err error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("date %q must be in YYYY-MM-DD form: %w", s, err)
	}
	return t.Year(), int(t.Month()), t.Day(), nil
}

// parseClock validates a 24-hour "HH:MM" time of day and returns its components.
func parseClock(s string) (hours, minutes int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("time %q must be in 24-hour HH:MM form: %w", s, err)
	}
	return t.Hour(), t.Minute(), nil
}

// itoa is a tiny readability alias for strconv.Itoa, used heavily when packing
// date/time components into an osascript argv.
func itoa(n int) string { return strconv.Itoa(n) }

// asFieldSep is the field delimiter a single-record probe script uses to join an
// item's captured properties. It is the ASCII Unit Separator (0x1f), chosen
// because — unlike tab or newline — it cannot plausibly occur inside a calendar
// summary, location, or notes body, so a multi-line notes field round-trips
// through capture-and-restore without being split or flattened. The AppleScript
// side emits it as `(character id 31)`.
const asFieldSep = "\x1f"

// ymdhm is a calendar date split into the integer components an AppleScript
// `_mkdate` call consumes, avoiding any locale-dependent date string parsing
// across the Go/AppleScript boundary.
type ymdhm struct {
	year, month, day, hours, minutes int
}

// args renders the components as the five consecutive argv strings _mkdate
// expects (year, month, day, hours, minutes).
func (t ymdhm) args() []string {
	return []string{itoa(t.year), itoa(t.month), itoa(t.day), itoa(t.hours), itoa(t.minutes)}
}

// parseFmtDateTime parses the "YYYY-MM-DD HH:MM" string a probe script emits
// (via _fmt) back into components, so a captured prior start/end time can be
// repacked into an inverse command's argv.
func parseFmtDateTime(s string) (ymdhm, error) {
	date, clock, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return ymdhm{}, fmt.Errorf("malformed date-time %q (want \"YYYY-MM-DD HH:MM\")", s)
	}
	y, mo, d, err := parseDate(date)
	if err != nil {
		return ymdhm{}, err
	}
	h, mi, err := parseClock(clock)
	if err != nil {
		return ymdhm{}, err
	}
	return ymdhm{y, mo, d, h, mi}, nil
}

// asRows splits an AppleScript script's stdout into non-empty rows. The scripts
// emit one record per line; a trailing newline (and any blank lines) are dropped.
func asRows(stdout string) []string {
	stdout = strings.TrimRight(stdout, "\n")
	if strings.TrimSpace(stdout) == "" {
		return nil
	}
	return strings.Split(stdout, "\n")
}
