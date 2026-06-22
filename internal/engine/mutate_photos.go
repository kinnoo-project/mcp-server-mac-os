// mutate_photos.go implements the cleanly-reversible Photos mutations — set_favorite,
// set_title, set_description, set_date, and set_keywords — as staged plans that
// follow the probe→restore pattern used by append_to_note (see mutate_notes.go).
//
// # Reversibility, the stage-time way
//
// Each of these changes ONE writable property of a single media item. At stage
// time the mutator probes the item's CURRENT value of that property and bakes it
// into the inverse command, so undo restores exactly the prior value. As with every
// mutator, both the forward and inverse commands are fully resolved at stage time
// and all model values cross into AppleScript as "--"-terminated `on run argv` data
// (via osascriptCommand), so a value beginning with "-" can never be parsed as an
// osascript option.
//
// # Why these five and not album/import operations
//
// These are the property writes Photos exposes that have a true inverse: the prior
// value is readable, so undo can put it back verbatim. Operations without a scripted
// inverse (creating albums/folders, adding to an album, importing) live in
// mutate_photos_weak.go and are staged with an explicit "no automatic undo" notice.
// Photos' AppleScript `delete` is restricted to albums/folders — it cannot touch a
// media item — so none of these can destroy a photo regardless of input.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// probePhotoScript returns a media item's name, favorite flag, description, and
// formatted date — the prior values the property mutators capture for their
// inverse and preview. argv: id. Fields (4) are joined by asFieldSep so a
// multi-word description round-trips intact. The raw values are read inside the
// tell block and formatted afterward, so the date/_str handlers need no `my`
// qualifier. A bad id makes the `whose id` lookup error (non-zero exit).
const probePhotoScript = asDateHelpers + `on run argv
	set theId to item 1 of argv
	set sep to (character id 31)
	set nm to ""
	set ds to ""
	set fav to "false"
	set rawDate to missing value
	tell application "Photos"
		set m to first media item whose id is theId
		set nm to name of m
		set ds to description of m
		set fav to (favorite of m) as string
		try
			set rawDate to date of m
		end try
	end tell
	set dt to ""
	if rawDate is not missing value then set dt to _fmt(rawDate)
	return _str(nm) & sep & fav & sep & _str(ds) & sep & dt
end run`

// probeKeywordsScript returns a media item's name and current keyword list. argv:
// id. The name is field 0; every keyword after it is its own asFieldSep field, so
// a keyword containing a comma or space is preserved. An item with no keywords
// yields just the name.
const probeKeywordsScript = asDateHelpers + `on run argv
	set theId to item 1 of argv
	set sep to (character id 31)
	set nm to ""
	set kw to {}
	tell application "Photos"
		set m to first media item whose id is theId
		set nm to name of m
		set kw to keywords of m
	end tell
	set savedTID to AppleScript's text item delimiters
	set AppleScript's text item delimiters to sep
	set kwJoined to kw as string
	set AppleScript's text item delimiters to savedTID
	if (count of kw) is 0 then return _str(nm)
	return _str(nm) & sep & kwJoined
end run`

// setFavoriteScript sets a media item's favorite flag. argv: id, "true"/"false".
const setFavoriteScript = `on run argv
	set theId to item 1 of argv
	set v to (item 2 of argv) is "true"
	tell application "Photos"
		set favorite of (first media item whose id is theId) to v
	end tell
end run`

// setNameScript sets a media item's title (name). argv: id, title.
const setNameScript = `on run argv
	set theId to item 1 of argv
	tell application "Photos"
		set name of (first media item whose id is theId) to (item 2 of argv)
	end tell
end run`

// setDescriptionScript sets a media item's description. argv: id, description.
const setDescriptionScript = `on run argv
	set theId to item 1 of argv
	tell application "Photos"
		set description of (first media item whose id is theId) to (item 2 of argv)
	end tell
end run`

// setDateScript sets a media item's date from integer components. argv: id, year,
// month, day, hours, minutes (the _mkdate order). _mkdate builds the date without
// any locale-dependent string parsing.
const setDateScript = asDateHelpers + `on run argv
	set theId to item 1 of argv
	set d to _mkdate(item 2 of argv, item 3 of argv, item 4 of argv, item 5 of argv, item 6 of argv)
	tell application "Photos"
		set date of (first media item whose id is theId) to d
	end tell
end run`

// setKeywordsScript REPLACES a media item's keyword list with the keywords passed
// as argv items 2..N (an empty tail clears all keywords). argv: id, keyword...
const setKeywordsScript = `on run argv
	set theId to item 1 of argv
	set kw to {}
	repeat with i from 2 to (count of argv)
		set end of kw to item i of argv
	end repeat
	tell application "Photos"
		set keywords of (first media item whose id is theId) to kw
	end tell
end run`

// The following xCommand helpers build the fully-tokenized osascript Command for
// each forward/inverse step. They exist as pure functions so the "--"-terminator
// hardening and argv ordering can be unit-tested with hostile (flag-like) values
// without running osascript or touching Photos (see mutate_photos_test.go).

// favoriteCommand sets favorite to the given "true"/"false" value.
func favoriteCommand(id, val string) Command { return osascriptCommand(setFavoriteScript, id, val) }

// nameCommand sets the title to name.
func nameCommand(id, name string) Command { return osascriptCommand(setNameScript, id, name) }

// descriptionCommand sets the description.
func descriptionCommand(id, desc string) Command {
	return osascriptCommand(setDescriptionScript, id, desc)
}

// dateCommand sets the date from the given components.
func dateCommand(id string, t ymdhm) Command {
	return osascriptCommand(setDateScript, append([]string{id}, t.args()...)...)
}

// keywordsCommand replaces the keyword list with kw.
func keywordsCommand(id string, kw []string) Command {
	return osascriptCommand(setKeywordsScript, append([]string{id}, kw...)...)
}

// priorPhoto holds the values probePhotoScript captures for a single item.
type priorPhoto struct {
	name, favorite, description, date string
}

// probePhoto runs the shared read-only probe and returns the item's current
// name/favorite/description/date. A bad id surfaces as a non-zero exit.
func probePhoto(ctx context.Context, op, id string) (priorPhoto, error) {
	res, err := runOsascript(ctx, probePhotoScript, id)
	if err != nil {
		return priorPhoto{}, err
	}
	if res.ExitCode != 0 {
		return priorPhoto{}, photosScriptError(op, res.Stderr)
	}
	f := strings.Split(strings.TrimSuffix(res.Stdout, "\n"), asFieldSep)
	if len(f) != 4 {
		return priorPhoto{}, fmt.Errorf("%s: unexpected probe output for id %q", op, id)
	}
	return priorPhoto{name: f[0], favorite: f[1], description: f[2], date: f[3]}, nil
}

// requireID extracts and validates the required media item id shared by every
// Photos mutator.
func requireID(op string, in map[string]any) (string, error) {
	id, _ := getString(in, "id")
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("%s: 'id' is required (from search_photos/list_photos)", op)
	}
	return id, nil
}

// photoLabel renders a probed item for a preview: its title, or the id when the
// item has no title.
func photoLabel(name, id string) string {
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("item %s", id)
	}
	return fmt.Sprintf("%q", name)
}

// stageSetFavorite stages a reversible favorite toggle: it probes the current flag
// so the inverse restores it.
func stageSetFavorite(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	id, err := requireID("set_favorite", in)
	if err != nil {
		return nil, err
	}
	want := getBool(in, "favorite")

	prior, err := probePhoto(ctx, "set_favorite", id)
	if err != nil {
		return nil, err
	}
	inverse := favoriteCommand(id, prior.favorite)
	return &StagedPlan{
		Preview: fmt.Sprintf("Set favorite of %s to %t (was %s).\n\nUndo restores the previous favorite state.",
			photoLabel(prior.name, id), want, prior.favorite),
		Forward: favoriteCommand(id, boolText(want)),
		Inverse: &inverse,
	}, nil
}

// stageSetTitle stages a reversible title change, capturing the prior title.
func stageSetTitle(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	id, err := requireID("set_title", in)
	if err != nil {
		return nil, err
	}
	title, ok := getString(in, "title")
	if !ok {
		return nil, fmt.Errorf("set_title: 'title' is required (use an empty string to clear it)")
	}
	prior, err := probePhoto(ctx, "set_title", id)
	if err != nil {
		return nil, err
	}
	inverse := nameCommand(id, prior.name)
	return &StagedPlan{
		Preview: fmt.Sprintf("Set title of %s to %s (was %s).\n\nUndo restores the previous title.",
			photoLabel(prior.name, id), quoteOrCleared(title), quoteOrCleared(prior.name)),
		Forward: nameCommand(id, title),
		Inverse: &inverse,
	}, nil
}

// stageSetDescription stages a reversible description change, capturing the prior
// description.
func stageSetDescription(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	id, err := requireID("set_description", in)
	if err != nil {
		return nil, err
	}
	desc, ok := getString(in, "description")
	if !ok {
		return nil, fmt.Errorf("set_description: 'description' is required (use an empty string to clear it)")
	}
	prior, err := probePhoto(ctx, "set_description", id)
	if err != nil {
		return nil, err
	}
	inverse := descriptionCommand(id, prior.description)
	return &StagedPlan{
		Preview: fmt.Sprintf("Set description of %s to %s (was %s).\n\nUndo restores the previous description.",
			photoLabel(prior.name, id), quoteOrCleared(desc), quoteOrCleared(prior.description)),
		Forward: descriptionCommand(id, desc),
		Inverse: &inverse,
	}, nil
}

// stageSetDate stages a reversible date change. The new date is parsed (a pure
// step, so a malformed date is rejected before any probe), and the prior date is
// captured for the inverse. An item with no current date is refused, because
// there would be nothing to restore on undo (keeping the reversibility promise).
func stageSetDate(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	id, err := requireID("set_date", in)
	if err != nil {
		return nil, err
	}
	dateStr, _ := getString(in, "date")
	timeStr, ok := getString(in, "time")
	if !ok || strings.TrimSpace(timeStr) == "" {
		timeStr = "00:00"
	}
	y, mo, d, err := parseDate(dateStr)
	if err != nil {
		return nil, fmt.Errorf("set_date: %w", err)
	}
	h, mi, err := parseClock(timeStr)
	if err != nil {
		return nil, fmt.Errorf("set_date: %w", err)
	}
	want := ymdhm{y, mo, d, h, mi}

	prior, err := probePhoto(ctx, "set_date", id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(prior.date) == "" {
		return nil, fmt.Errorf("set_date: %s has no current date, so the change could not be undone; refusing to proceed", photoLabel(prior.name, id))
	}
	priorYMDHM, err := parseFmtDateTime(prior.date)
	if err != nil {
		return nil, fmt.Errorf("set_date: could not read the item's current date (%q): %w", prior.date, err)
	}
	inverse := dateCommand(id, priorYMDHM)
	return &StagedPlan{
		Preview: fmt.Sprintf("Set date of %s to %s (was %s).\n\nUndo restores the previous date.",
			photoLabel(prior.name, id), fmtYMDHM(want), prior.date),
		Forward: dateCommand(id, want),
		Inverse: &inverse,
	}, nil
}

// stageSetKeywords stages a reversible keyword replacement, capturing the prior
// keyword list so the inverse restores it exactly.
func stageSetKeywords(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	id, err := requireID("set_keywords", in)
	if err != nil {
		return nil, err
	}
	newKw, ok := getStringList(in, "keywords")
	if !ok {
		return nil, fmt.Errorf("set_keywords: 'keywords' is required (use an empty list to clear all keywords)")
	}
	name, priorKw, err := probeKeywords(ctx, id)
	if err != nil {
		return nil, err
	}
	inverse := keywordsCommand(id, priorKw)
	return &StagedPlan{
		Preview: fmt.Sprintf("Replace keywords of %s with %s (was %s).\n\nUndo restores the previous keywords.",
			photoLabel(name, id), keywordsPreview(newKw), keywordsPreview(priorKw)),
		Forward: keywordsCommand(id, newKw),
		Inverse: &inverse,
	}, nil
}

// probeKeywords runs the keyword probe and returns the item's name and current
// keyword list. A bad id surfaces as a non-zero exit.
func probeKeywords(ctx context.Context, id string) (name string, keywords []string, err error) {
	res, err := runOsascript(ctx, probeKeywordsScript, id)
	if err != nil {
		return "", nil, err
	}
	if res.ExitCode != 0 {
		return "", nil, photosScriptError("set_keywords", res.Stderr)
	}
	f := strings.Split(strings.TrimSuffix(res.Stdout, "\n"), asFieldSep)
	// Field 0 is the name; any remaining fields are the keywords (none when the
	// item has no keywords, in which case the script returns only the name).
	return f[0], f[1:], nil
}

// quoteOrCleared renders a string value for a preview, distinguishing an explicit
// empty value ("(cleared)") from a quoted non-empty one.
func quoteOrCleared(s string) string {
	if s == "" {
		return "(cleared)"
	}
	return fmt.Sprintf("%q", s)
}

// keywordsPreview renders a keyword list for a preview, distinguishing the empty
// case from a bracketed list.
func keywordsPreview(kw []string) string {
	if len(kw) == 0 {
		return "(none)"
	}
	return "[" + strings.Join(kw, ", ") + "]"
}
