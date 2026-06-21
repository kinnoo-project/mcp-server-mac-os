// builtins_notes.go implements the read-only Notes operations — list_notes,
// search_notes, read_note, and list_folders — by driving Notes.app through its
// AppleScript dictionary via the hardened osascript path in applescript.go, then
// formatting the script's delimited stdout into human-readable text in Go.
//
// # Why these are AppleScript builtins (like Calendar), not sqlite reads
//
// Notes stores its data in a Core Data store that has no stable, documented read
// schema (and locked notes are encrypted at rest), so — unlike Messages, whose
// history we read straight from chat.db — the supported way to read notes is the
// app's scripting dictionary. The work is therefore a fixed AppleScript program
// plus result parsing, which is exactly the shape of the Calendar reads, so these
// live here as builtins answered in-process rather than as generic argv builders.
//
// # Two Notes-specific guardrails
//
//   - Plain text, never HTML. A note's `body` is HTML; the dictionary also exposes
//     a read-only `plaintext` property. Reads use `plaintext` so no HTML has to be
//     parsed in Go and the model sees clean text. (The mutators in mutate_notes.go
//     DO touch `body`, because writing requires HTML.)
//   - Locked notes degrade, never error. A password-protected note that is locked
//     errors on `plaintext`/`body` access. Every per-note text access is wrapped in
//     an AppleScript `try` so one locked note renders as "(locked note)" and the
//     listing/read continues, mirroring the calendar "malformed row shown raw"
//     defensiveness rather than failing the whole operation.
//
// As with Calendar, the first call triggers a one-time macOS automation-permission
// prompt for controlling Notes; notesScriptError turns a denial into a hint.
package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// defaultNoteLimit / maxNoteLimit bound how many notes a listing returns: a
// default that keeps output readable and a hard ceiling a caller cannot exceed,
// matching the Calendar/Reminders read limits. Note metadata (id, title, folder,
// modification date) is cheap to enumerate, so the AppleScript fetches all of it
// and the limit is applied in Go AFTER sorting newest-first — that way the cap
// keeps the genuinely most-recently-modified notes rather than an arbitrary slice
// of whatever order the dictionary yields. Note BODIES are never read by a
// listing (only read_note reads a body), so enumeration stays light.
const (
	defaultNoteLimit = 25
	maxNoteLimit     = 100
)

// listNotesScript emits one tab-delimited metadata row per note:
//
//	id \t title \t "YYYY-MM-DD HH:MM" modification-date \t folder
//
// argv (after the "--" terminator): folderFilter. An empty folderFilter means
// "all folders". Helper calls inside the tell block are prefixed with `my` so
// they resolve to this script's _clean/_fmt handlers, not Notes' dictionary. Only
// metadata is read here, so a locked note still lists fine.
const listNotesScript = asDateHelpers + `on run argv
	set folderFilter to item 1 of argv
	set out to ""
	tell application "Notes"
		if folderFilter is "" then
			set theNotes to every note
		else
			set theNotes to every note of (first folder whose name is folderFilter)
		end if
		repeat with n in theNotes
			set out to out & (id of n) & tab & my _clean(name of n) & tab & my _fmt(modification date of n) & tab & my _clean(name of container of n) & linefeed
		end repeat
	end tell
	return out
end run`

// searchNotesScript emits the same metadata rows as listNotesScript, but only for
// notes whose title OR plain-text body contains the query. argv: query,
// folderFilter. The plain-text read is wrapped in `try` so a locked note is
// simply matched on its (still-readable) title instead of erroring. AppleScript
// text comparison ignores case by default, so the match is case-insensitive
// without lowercasing.
//
// An empty folderFilter searches every note; a non-empty one confines the search
// to that one folder. The scoping matters for cost, not just convenience: the
// match reads each candidate note's full `plaintext`, so the work scales with the
// number of notes scanned. Restricting to a folder bounds that to the folder's
// size instead of the whole library — the resource-conscious path on a large
// Notes store.
const searchNotesScript = asDateHelpers + `on run argv
	set q to item 1 of argv
	set folderFilter to item 2 of argv
	set out to ""
	tell application "Notes"
		if folderFilter is "" then
			set theNotes to every note
		else
			set theNotes to every note of (first folder whose name is folderFilter)
		end if
		repeat with n in theNotes
			set hay to (name of n)
			try
				set hay to hay & " " & (plaintext of n)
			end try
			if hay contains q then
				set out to out & (id of n) & tab & my _clean(name of n) & tab & my _fmt(modification date of n) & tab & my _clean(name of container of n) & linefeed
			end if
		end repeat
	end tell
	return out
end run`

// readNoteScript returns a single note's title and plain-text body, joined by
// asFieldSep so a multi-line body round-trips without being split. argv: id. A
// locked note yields the body "(locked note)" via the `try` fallback rather than
// erroring; a bad id makes `first note whose id is …` error (non-zero exit).
const readNoteScript = `on run argv
	set theId to item 1 of argv
	set sep to (character id 31)
	tell application "Notes"
		set n to first note whose id is theId
		set theTitle to name of n
		set theBody to "(locked note)"
		try
			set theBody to plaintext of n
		end try
		return theTitle & sep & theBody
	end tell
end run`

// listFoldersScript returns one folder name per line, across all accounts.
const listFoldersScript = `on run argv
	set out to ""
	tell application "Notes"
		repeat with f in folders
			set out to out & (name of f) & linefeed
		end repeat
	end tell
	return out
end run`

// noteMeta is one parsed metadata row from listNotesScript/searchNotesScript.
type noteMeta struct {
	id, title, modified, folder string
}

// runListNotes lists notes (most recently modified first), optionally restricted
// to one folder, capped at limit.
func runListNotes(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	folder, _ := getString(in, "folder")
	res, err := runOsascript(ctx, listNotesScript, folder)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", notesScriptError("list_notes", res.Stderr)
	}
	notes := parseNoteRows(res.Stdout)
	if len(notes) == 0 {
		if folder != "" {
			return fmt.Sprintf("No notes found in folder %q.", folder), nil
		}
		return "No notes found.", nil
	}
	notes = sortAndCapNotes(notes, cappedNoteLimit(in))
	return renderNoteList(fmt.Sprintf("%d note(s), most recently modified first:", len(notes)), notes), nil
}

// runSearchNotes lists notes whose title or body contains the query, newest
// first. An optional folder confines the search (and so the per-note body reads)
// to a single folder, the lighter path on a large library.
func runSearchNotes(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	query, _ := getString(in, "query")
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("search_notes: 'query' is required")
	}
	folder, _ := getString(in, "folder")
	res, err := runOsascript(ctx, searchNotesScript, query, folder)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", notesScriptError("search_notes", res.Stderr)
	}
	notes := parseNoteRows(res.Stdout)
	if len(notes) == 0 {
		if folder != "" {
			return fmt.Sprintf("No notes in folder %q match %q.", folder, query), nil
		}
		return fmt.Sprintf("No notes found matching %q.", query), nil
	}
	notes = sortAndCapNotes(notes, cappedNoteLimit(in))
	heading := fmt.Sprintf("%d note(s) matching %q, most recently modified first:", len(notes), query)
	if folder != "" {
		heading = fmt.Sprintf("%d note(s) in folder %q matching %q, most recently modified first:", len(notes), folder, query)
	}
	return renderNoteList(heading, notes), nil
}

// runReadNote returns one note's full plain-text body (bounded by the shared
// output budget so a very long note can't saturate the model's context).
func runReadNote(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	id, _ := getString(in, "id")
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("read_note: 'id' is required (from list_notes/search_notes)")
	}
	res, err := runOsascript(ctx, readNoteScript, id)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", notesScriptError("read_note", res.Stderr)
	}
	// title <sep> body; SplitN keeps a body that itself contains nothing but the
	// (0x1f) separator intact, though HTML/plaintext never contains 0x1f.
	// osascript prints a single trailing newline after the returned string; strip
	// exactly that one (TrimSuffix, not TrimRight) so a note whose plaintext
	// intentionally ends in blank lines is not silently truncated — same rationale
	// as probeNoteBody.
	parts := strings.SplitN(strings.TrimSuffix(res.Stdout, "\n"), asFieldSep, 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("read_note: unexpected probe output for id %q", id)
	}
	title, body := parts[0], parts[1]
	if strings.TrimSpace(body) == "" {
		body = "(empty note)"
	}
	return compactOutput(fmt.Sprintf("%s\n\n%s", title, body)), nil
}

// runListFolders lists the note folders by name.
func runListFolders(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	res, err := runOsascript(ctx, listFoldersScript)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", notesScriptError("list_folders", res.Stderr)
	}
	names := asRows(res.Stdout)
	if len(names) == 0 {
		return "No note folders found.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Note folders (%d):\n", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "  - %s\n", strings.TrimSpace(n))
	}
	return b.String(), nil
}

// parseNoteRows splits the tab-delimited metadata rows from a list/search script
// into noteMeta values. A row with the wrong field count is skipped defensively
// so one odd record can't corrupt the whole listing.
func parseNoteRows(stdout string) []noteMeta {
	var out []noteMeta
	for _, row := range asRows(stdout) {
		f := strings.Split(row, "\t")
		if len(f) != 4 {
			continue
		}
		out = append(out, noteMeta{id: f[0], title: f[1], modified: f[2], folder: f[3]})
	}
	return out
}

// sortAndCapNotes orders notes most-recently-modified first and trims to limit.
// The modification field is "YYYY-MM-DD HH:MM", which sorts lexicographically in
// chronological order, so a descending string sort is a descending time sort.
func sortAndCapNotes(notes []noteMeta, limit int) []noteMeta {
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].modified > notes[j].modified })
	if len(notes) > limit {
		notes = notes[:limit]
	}
	return notes
}

// renderNoteList formats note metadata under a heading, one note per block with
// its id on a second line (the id is what read_note/append_to_note consume).
func renderNoteList(heading string, notes []noteMeta) string {
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for i, n := range notes {
		fmt.Fprintf(&b, "%2d. %s  [%s]  (modified %s)\n      id: %s\n",
			i+1, orUntitled(n.title), orUnknown(n.folder), n.modified, n.id)
	}
	return b.String()
}

// orUntitled renders an empty note title as a clear placeholder.
func orUntitled(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(untitled)"
	}
	return s
}

// cappedNoteLimit reads the "limit" parameter, applying the notes default and the
// hard ceiling.
func cappedNoteLimit(in map[string]any) int {
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultNoteLimit
	}
	if limit > maxNoteLimit {
		limit = maxNoteLimit
	}
	return limit
}

// notesScriptError wraps an osascript failure with a hint about the most common
// cause — automation access to Notes not yet granted — so the caller gets an
// actionable message instead of a bare AppleScript error number. Mirrors
// calendarScriptError.
func notesScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: Notes automation failed (%s). If this is the first use, macOS may need you to grant this app access to Notes in System Settings → Privacy & Security → Automation", op, stderr)
}
