// mutate_notes.go implements the mutating Notes operations — create_note and
// append_to_note — as reversible staged plans, mirroring mutate_reminders.go.
//
// # Writing to Notes means writing HTML
//
// Unlike the reads (which use the clean `plaintext` property), a note's content
// is set through its `body` property, which is HTML. So the mutators build HTML
// in Go via noteBodyHTML: model-supplied title/body text is HTML-escaped (so a
// literal '<', '>' or '&' cannot corrupt the markup) and newlines become <br>.
// Notes derives a note's displayed title from the first line of its body, so a
// created note's title is emitted as the first <div>.
//
// # Reversibility, the stage-time way
//
// As with every mutator, BOTH the forward and inverse commands are resolved at
// stage time (see mutate.go) and all model values cross into AppleScript as
// "--"-terminated `on run argv` data:
//
//   - create_note's inverse deletes the note by its title, exactly as
//     add_reminder's inverse deletes by name. The new note's internal id does not
//     exist until commit, so — like reminders — the compensating delete matches on
//     title, with the documented caveat that a pre-existing same-title note could
//     be the one removed by undo.
//   - append_to_note probes the note's current `body` at stage time; the forward
//     sets body to prior+appended and the inverse sets it straight back, so undo
//     restores the exact prior contents.
package engine

import (
	"context"
	"fmt"
	"html"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// createNoteScript makes one note, in a named folder or (empty folder) the
// default folder. argv: folder, bodyHTML. It is create_note's forward command.
const createNoteScript = `on run argv
	set folderName to item 1 of argv
	set bodyHTML to item 2 of argv
	tell application "Notes"
		if folderName is "" then
			make new note with properties {body:bodyHTML}
		else
			make new note at (first folder whose name is folderName) with properties {body:bodyHTML}
		end if
	end tell
end run`

// deleteNoteByTitleScript removes a single note matched by title, in a named
// folder or across all folders. argv: folder, title. It is create_note's inverse;
// only the first match is deleted, so a pre-existing same-title note is the most
// that can be wrongly removed.
const deleteNoteByTitleScript = `on run argv
	set folderName to item 1 of argv
	set theTitle to item 2 of argv
	tell application "Notes"
		if folderName is "" then
			set matches to (every note whose name is theTitle)
		else
			set matches to (every note of (first folder whose name is folderName) whose name is theTitle)
		end if
		if (count of matches) > 0 then delete (item 1 of matches)
	end tell
end run`

// probeNoteBodyScript returns a note's title and current HTML body, joined by
// asFieldSep (so the multi-line HTML survives). argv: id. A bad id, or a locked
// note whose body cannot be read, surfaces as a non-zero exit so append refuses
// to stage rather than producing a broken inverse.
const probeNoteBodyScript = `on run argv
	set theId to item 1 of argv
	set sep to (character id 31)
	tell application "Notes"
		set n to first note whose id is theId
		return (name of n) & sep & (body of n)
	end tell
end run`

// setNoteBodyScript sets a note's body (HTML) by id. argv: id, bodyHTML. It backs
// both append_to_note's forward (prior+appended) and its inverse (prior body).
const setNoteBodyScript = `on run argv
	set theId to item 1 of argv
	set newBody to item 2 of argv
	tell application "Notes"
		set body of (first note whose id is theId) to newBody
	end tell
end run`

// noteBodyHTML builds the HTML for a brand-new note: the title as a bold first
// line (which becomes the note's displayed title) followed by the body. Both are
// HTML-escaped and newlines become <br> so the text renders verbatim and cannot
// inject markup.
func noteBodyHTML(title, body string) string {
	markup := "<div><b>" + escapeNoteHTML(title) + "</b></div>"
	if strings.TrimSpace(body) != "" {
		markup += "<div>" + escapeNoteHTML(body) + "</div>"
	}
	return markup
}

// appendedHTML builds the HTML fragment appended to an existing note: the new
// text as its own escaped block.
func appendedHTML(text string) string { return "<div>" + escapeNoteHTML(text) + "</div>" }

// escapeNoteHTML makes arbitrary text safe to embed in a note's HTML body:
// HTML-escapes the metacharacters, then turns newlines into <br> so multi-line
// input keeps its line breaks instead of collapsing.
func escapeNoteHTML(s string) string {
	return strings.ReplaceAll(html.EscapeString(s), "\n", "<br>")
}

// stageCreateNote stages a reversible note creation. The inverse deletes the note
// by title (see file header for the new-id rationale).
func stageCreateNote(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	title, _ := getString(in, "title")
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("create_note: 'title' is required")
	}
	body, _ := getString(in, "body")
	folder, _ := getString(in, "folder")

	inverse := osascriptCommand(deleteNoteByTitleScript, folder, title)
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Create note titled %q%s%s.\n\nUndo will delete the note titled %q (note: if another note with that title exists, undo removes the first match).",
			title, noteFolderPreview(folder), noteBodyPreview(body), title,
		),
		Forward: osascriptCommand(createNoteScript, folder, noteBodyHTML(title, body)),
		Inverse: &inverse,
	}, nil
}

// stageAppendToNote stages a reversible append: it probes the note's current body
// so the inverse can restore it verbatim.
func stageAppendToNote(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	id, _ := getString(in, "id")
	text, _ := getString(in, "text")
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("append_to_note: 'id' is required (from list_notes/search_notes)")
	}
	if text == "" {
		return nil, fmt.Errorf("append_to_note: 'text' is required")
	}

	title, priorBody, err := probeNoteBody(ctx, id)
	if err != nil {
		return nil, err
	}

	inverse := osascriptCommand(setNoteBodyScript, id, priorBody)
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Append to note %q (id %s):\n  + %s\n\nUndo will restore the note's previous contents.",
			title, id, previewText(text),
		),
		Forward: osascriptCommand(setNoteBodyScript, id, priorBody+appendedHTML(text)),
		Inverse: &inverse,
	}, nil
}

// probeNoteBody runs the read-only probe and returns the note's title and current
// HTML body. A bad id or unreadable (locked) note surfaces as a non-zero exit.
func probeNoteBody(ctx context.Context, id string) (title, body string, err error) {
	res, err := runOsascript(ctx, probeNoteBodyScript, id)
	if err != nil {
		return "", "", err
	}
	if res.ExitCode != 0 {
		return "", "", fmt.Errorf("append_to_note: could not read note id %q (%s). A locked note cannot be appended to", id, strings.TrimSpace(res.Stderr))
	}
	// title <sep> body. osascript prints a single trailing newline after the
	// returned string; strip exactly that one so the captured body — which may
	// itself end in markup — round-trips for the inverse.
	parts := strings.SplitN(strings.TrimSuffix(res.Stdout, "\n"), asFieldSep, 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("append_to_note: unexpected probe output for id %q", id)
	}
	return parts[0], parts[1], nil
}

// noteFolderPreview renders the optional folder clause for a create preview.
func noteFolderPreview(folder string) string {
	if strings.TrimSpace(folder) == "" {
		return " in the default folder"
	}
	return fmt.Sprintf(" in folder %q", folder)
}

// noteBodyPreview renders the optional body tail for a create preview.
func noteBodyPreview(body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return fmt.Sprintf(" with body: %s", previewText(body))
}
