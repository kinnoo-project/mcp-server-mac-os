// builtins_photos.go implements the read-only Photos operations — search_photos,
// list_photos, get_photo, list_albums, list_photo_folders, get_album_photos,
// list_favorites, list_recently_deleted, get_selection, and library_stats — by
// driving Photos.app through its AppleScript dictionary via the hardened osascript
// path in applescript.go, then formatting the script's delimited stdout into
// human-readable text in Go.
//
// # Why these are AppleScript builtins (like Notes/Calendar)
//
// The Photos library lives in a private, undocumented SQLite store inside the
// .photoslibrary package; the supported, stable way to read it is the app's
// scripting dictionary. So — exactly like Notes — each read is a fixed AppleScript
// program plus result parsing, which is the shape of a builtin answered in-process
// rather than a generic argv builder.
//
// # Two Photos-specific design choices
//
//   - Bounded enumeration, never the whole library. A listing reads only a small
//     range of media items (`media items 1 thru N`), capped by maxPhotoLimit, so a
//     huge library can never force the script to materialize tens of thousands of
//     items. search_photos delegates to Photos' OWN search index — the powerful,
//     fast path — and likewise caps the result it formats. The Photos `delete`
//     verb applies only to albums/folders (never media items), so nothing here can
//     destroy a photo regardless of input.
//   - GPS only on the single-item read. Photos carry precise coordinates, which are
//     privacy-sensitive. The bulk row format deliberately omits location; only
//     get_photo (one explicit item) reports latitude/longitude and altitude.
//
// As with Notes, the first call triggers a one-time macOS Automation prompt for
// controlling Photos; photosScriptError turns a denial into an actionable hint.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// defaultPhotoLimit / maxPhotoLimit bound how many media items a listing returns:
// a readable default and a hard ceiling a caller cannot exceed, matching the
// Notes/Calendar read limits. The cap is also what keeps enumeration cheap — the
// AppleScript only ever touches `media items 1 thru N`, so the work is bounded by
// the limit, not by the size of the library.
const (
	defaultPhotoLimit = 25
	maxPhotoLimit     = 100
)

// photoRowHelpers prepends the shared date/text handlers (asDateHelpers) plus a
// `_row` handler that renders one media item as a single tab-delimited metadata
// row: id \t title \t filename \t "YYYY-MM-DD HH:MM" \t favorite \t WxH. Every
// list/search script reuses `my _row(m)` so the row contract is defined once.
//
// Each field is defended: name/filename/description go through _str (missing
// value → "") then _clean (tabs/newlines → spaces) so one odd item cannot corrupt
// the one-row-per-line contract, and the date read is wrapped in `try` so an item
// without a readable date renders with an empty date instead of failing the whole
// listing. Location is intentionally NOT part of a row (see file header).
const photoRowHelpers = asDateHelpers + `on _row(m)
	set theDate to ""
	try
		set theDate to _fmt(date of m)
	end try
	return (id of m) & tab & _clean(_str(name of m)) & tab & _clean(_str(filename of m)) & tab & theDate & tab & ((favorite of m) as string) & tab & ((width of m) as string) & "x" & ((height of m) as string)
end _row

`

// listPhotosScript emits one _row per media item for the first `lim` items in the
// library's native order. argv: limit. An empty library returns "".
const listPhotosScript = photoRowHelpers + `on run argv
	set lim to (item 1 of argv) as integer
	set out to ""
	tell application "Photos"
		set cnt to count of media items
		if cnt is 0 then return ""
		if lim > cnt then set lim to cnt
		repeat with m in (media items 1 thru lim)
			set out to out & my _row(m) & linefeed
		end repeat
	end tell
	return out
end run`

// searchPhotosScript runs Photos' native search for the query and emits one _row
// per match, capped at `lim`. argv: query, limit. This is the same search the
// in-app Search field performs, so it spans scenes/places/dates/text, not just
// filenames.
const searchPhotosScript = photoRowHelpers + `on run argv
	set q to item 1 of argv
	set lim to (item 2 of argv) as integer
	set out to ""
	tell application "Photos"
		set found to search for q
		set cnt to count of found
		if cnt is 0 then return ""
		if lim > cnt then set lim to cnt
		repeat with i from 1 to lim
			set out to out & my _row(item i of found) & linefeed
		end repeat
	end tell
	return out
end run`

// albumPhotosScript emits one _row per media item in a named album, capped at
// `lim`. argv: albumName, limit. A missing album makes `first album whose name
// is …` error (non-zero exit), which photosScriptError reports.
const albumPhotosScript = photoRowHelpers + `on run argv
	set albName to item 1 of argv
	set lim to (item 2 of argv) as integer
	set out to ""
	tell application "Photos"
		set theAlbum to first album whose name is albName
		set cnt to count of media items of theAlbum
		if cnt is 0 then return ""
		if lim > cnt then set lim to cnt
		repeat with m in (media items 1 thru lim of theAlbum)
			set out to out & my _row(m) & linefeed
		end repeat
	end tell
	return out
end run`

// favoritesScript and recentlyDeletedScript emit rows from the two special albums
// Photos exposes as application properties. argv: limit.
const favoritesScript = photoRowHelpers + `on run argv
	set lim to (item 1 of argv) as integer
	set out to ""
	tell application "Photos"
		set theAlbum to favorites album
		set cnt to count of media items of theAlbum
		if cnt is 0 then return ""
		if lim > cnt then set lim to cnt
		repeat with m in (media items 1 thru lim of theAlbum)
			set out to out & my _row(m) & linefeed
		end repeat
	end tell
	return out
end run`

const recentlyDeletedScript = photoRowHelpers + `on run argv
	set lim to (item 1 of argv) as integer
	set out to ""
	tell application "Photos"
		set theAlbum to recently deleted album
		set cnt to count of media items of theAlbum
		if cnt is 0 then return ""
		if lim > cnt then set lim to cnt
		repeat with m in (media items 1 thru lim of theAlbum)
			set out to out & my _row(m) & linefeed
		end repeat
	end tell
	return out
end run`

// selectionScript emits rows for the items currently selected in the Photos UI,
// capped at maxPhotoLimit. argv: limit. No selection returns "".
const selectionScript = photoRowHelpers + `on run argv
	set lim to (item 1 of argv) as integer
	set out to ""
	tell application "Photos"
		set sel to selection
		set cnt to count of sel
		if cnt is 0 then return ""
		if lim > cnt then set lim to cnt
		repeat with i from 1 to lim
			set out to out & my _row(item i of sel) & linefeed
		end repeat
	end tell
	return out
end run`

// listAlbumsScript emits one row per user album: id \t name \t itemCount.
const listAlbumsScript = asDateHelpers + `on run argv
	set out to ""
	tell application "Photos"
		repeat with a in albums
			set out to out & (id of a) & tab & my _clean(my _str(name of a)) & tab & ((count of media items of a) as string) & linefeed
		end repeat
	end tell
	return out
end run`

// listFoldersScript emits one row per folder: id \t name. The name is run through
// _clean/_str so a folder name containing a tab/newline cannot corrupt the
// one-row-per-line, tab-delimited contract parsePhotoRows' callers rely on.
const photoFoldersScript = asDateHelpers + `on run argv
	set out to ""
	tell application "Photos"
		repeat with f in folders
			set out to out & (id of f) & tab & my _clean(my _str(name of f)) & linefeed
		end repeat
	end tell
	return out
end run`

// statsScript emits the three library totals, one per line, in a fixed order:
// media items, albums, folders.
const statsScript = `on run argv
	tell application "Photos"
		set n1 to (count of media items) as string
		set n2 to (count of albums) as string
		set n3 to (count of folders) as string
	end tell
	return n1 & linefeed & n2 & linefeed & n3
end run`

// getPhotoScript returns a single media item's full metadata as one asFieldSep
// (0x1f) delimited record, so a multi-word description/keyword list round-trips
// without being split. argv: id. Field order (13):
//
//	id, title, filename, date, favorite, width, height, size,
//	altitude, latitude, longitude, description, keywords
//
// GPS and altitude are read defensively: an item without a location yields empty
// lat/lon rather than erroring. This is the ONLY script that emits coordinates.
const getPhotoScript = asDateHelpers + `on run argv
	set theId to item 1 of argv
	set sep to (character id 31)
	tell application "Photos"
		set m to first media item whose id is theId
		set theDate to ""
		try
			set theDate to _fmt(date of m)
		end try
		set theSize to ""
		try
			set theSize to (size of m) as string
		end try
		set theAlt to ""
		try
			set theAlt to (altitude of m) as string
		end try
		set latStr to ""
		set lonStr to ""
		try
			set loc to location of m
			if (item 1 of loc) is not missing value then set latStr to (item 1 of loc) as string
			if (item 2 of loc) is not missing value then set lonStr to (item 2 of loc) as string
		end try
		set kwStr to ""
		try
			set savedTID to AppleScript's text item delimiters
			set AppleScript's text item delimiters to ", "
			set kwStr to (keywords of m) as string
			set AppleScript's text item delimiters to savedTID
		end try
		return (id of m) & sep & _clean(_str(name of m)) & sep & _clean(_str(filename of m)) & sep & theDate & sep & ((favorite of m) as string) & sep & ((width of m) as string) & sep & ((height of m) as string) & sep & theSize & sep & theAlt & sep & latStr & sep & lonStr & sep & _clean(_str(description of m)) & sep & _clean(kwStr)
	end tell
end run`

// photoRow is one parsed tab-delimited metadata row from a list/search script.
type photoRow struct {
	id, title, filename, date, favorite, dims string
}

// runListPhotos lists a bounded slice of the library in native order.
func runListPhotos(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	return runPhotoListing(ctx, "list_photos", listPhotosScript,
		"No photos found.", "%d item(s) (library order, capped):", in,
		[]string{itoa(cappedPhotoLimit(in))})
}

// runSearchPhotos runs Photos' native search and lists the matches.
func runSearchPhotos(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	query, _ := getString(in, "query")
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("search_photos: 'query' is required")
	}
	return runPhotoListing(ctx, "search_photos", searchPhotosScript,
		fmt.Sprintf("No photos found matching %q.", query),
		fmt.Sprintf("%%d item(s) matching %q:", query), in,
		[]string{query, itoa(cappedPhotoLimit(in))})
}

// runGetAlbumPhotos lists a bounded slice of one named album.
func runGetAlbumPhotos(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	album, _ := getString(in, "album")
	if strings.TrimSpace(album) == "" {
		return "", fmt.Errorf("get_album_photos: 'album' is required (see list_albums)")
	}
	return runPhotoListing(ctx, "get_album_photos", albumPhotosScript,
		fmt.Sprintf("Album %q is empty.", album),
		fmt.Sprintf("%%d item(s) in album %q:", album), in,
		[]string{album, itoa(cappedPhotoLimit(in))})
}

// runListFavorites lists a bounded slice of the Favorites album.
func runListFavorites(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	return runPhotoListing(ctx, "list_favorites", favoritesScript,
		"No favorite photos found.", "%d favorite item(s):", in,
		[]string{itoa(cappedPhotoLimit(in))})
}

// runListRecentlyDeleted lists a bounded slice of the Recently Deleted album.
func runListRecentlyDeleted(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	return runPhotoListing(ctx, "list_recently_deleted", recentlyDeletedScript,
		"Recently Deleted is empty.", "%d item(s) in Recently Deleted (recoverable for 30 days):", in,
		[]string{itoa(cappedPhotoLimit(in))})
}

// runGetSelection lists the items currently selected in the Photos UI.
func runGetSelection(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	return runPhotoListing(ctx, "get_selection", selectionScript,
		"Nothing is selected in Photos.", "%d selected item(s):", in,
		[]string{itoa(maxPhotoLimit)})
}

// runPhotoListing is the shared body for every row-listing read: run the script
// with the given argv, surface an automation/permission failure as an actionable
// error, and either report the empty message or render the parsed rows under a
// heading. headingFmt must contain exactly one %d (the item count).
func runPhotoListing(ctx context.Context, op, script, emptyMsg, headingFmt string, _ map[string]any, argv []string) (string, error) {
	res, err := runOsascript(ctx, script, argv...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", photosScriptError(op, res.Stderr)
	}
	rows := parsePhotoRows(res.Stdout)
	if len(rows) == 0 {
		return emptyMsg, nil
	}
	return renderPhotoList(fmt.Sprintf(headingFmt, len(rows)), rows), nil
}

// runListAlbums lists the user albums with their item counts.
func runListAlbums(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	res, err := runOsascript(ctx, listAlbumsScript)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", photosScriptError("list_albums", res.Stderr)
	}
	rows := asRows(res.Stdout)
	if len(rows) == 0 {
		return "No albums found.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Albums (%d):\n", len(rows))
	for _, row := range rows {
		f := strings.Split(row, "\t")
		if len(f) != 3 {
			continue
		}
		fmt.Fprintf(&b, "  - %s  (%s items)\n      id: %s\n", orUntitled(f[1]), f[2], f[0])
	}
	return b.String(), nil
}

// runListPhotoFolders lists the library's folders by name.
func runListPhotoFolders(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	res, err := runOsascript(ctx, photoFoldersScript)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", photosScriptError("list_photo_folders", res.Stderr)
	}
	rows := asRows(res.Stdout)
	if len(rows) == 0 {
		return "No folders found.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Folders (%d):\n", len(rows))
	for _, row := range rows {
		f := strings.Split(row, "\t")
		if len(f) != 2 {
			continue
		}
		fmt.Fprintf(&b, "  - %s\n      id: %s\n", orUntitled(f[1]), f[0])
	}
	return b.String(), nil
}

// runLibraryStats reports the media-item, album, and folder totals.
func runLibraryStats(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	res, err := runOsascript(ctx, statsScript)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", photosScriptError("library_stats", res.Stderr)
	}
	rows := asRows(res.Stdout)
	if len(rows) != 3 {
		return "", fmt.Errorf("library_stats: unexpected output %q", res.Stdout)
	}
	return fmt.Sprintf("Photos library:\n  media items: %s\n  albums: %s\n  folders: %s",
		rows[0], rows[1], rows[2]), nil
}

// runGetPhoto reads one media item's full metadata, including GPS when present.
func runGetPhoto(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	id, _ := getString(in, "id")
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("get_photo: 'id' is required (from search_photos/list_photos)")
	}
	res, err := runOsascript(ctx, getPhotoScript, id)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", photosScriptError("get_photo", res.Stderr)
	}
	// 13 fields joined by asFieldSep. Strip the single trailing newline osascript
	// appends after the returned string so it does not bleed into the last field
	// (keywords); the script already runs every text field through _clean, so no
	// field contains an embedded newline of its own.
	f := strings.Split(strings.TrimSuffix(res.Stdout, "\n"), asFieldSep)
	if len(f) != 13 {
		return "", fmt.Errorf("get_photo: unexpected metadata for id %q", id)
	}
	return renderPhotoDetail(f), nil
}

// parsePhotoRows splits the tab-delimited rows from a list/search script into
// photoRow values. A row with the wrong field count is skipped defensively so one
// odd record cannot corrupt the whole listing.
func parsePhotoRows(stdout string) []photoRow {
	var out []photoRow
	for _, row := range asRows(stdout) {
		f := strings.Split(row, "\t")
		if len(f) != 6 {
			continue
		}
		out = append(out, photoRow{id: f[0], title: f[1], filename: f[2], date: f[3], favorite: f[4], dims: f[5]})
	}
	return out
}

// renderPhotoList formats photo rows under a heading, one item per block with its
// id on a second line (the id is what get_photo/export_photo consume).
func renderPhotoList(heading string, rows []photoRow) string {
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n")
	for i, r := range rows {
		star := ""
		if r.favorite == "true" {
			star = " ★"
		}
		fmt.Fprintf(&b, "%2d. %s%s  [%s]  %s px  (%s)\n      id: %s\n",
			i+1, orUntitled(r.title), star, r.filename, r.dims, r.date, r.id)
	}
	return b.String()
}

// renderPhotoDetail formats the 13-field get_photo record into a labeled block.
// Empty optional fields (location, altitude, description, keywords) are shown as a
// clear placeholder rather than omitted, so the reader can tell "absent" from
// "unread".
func renderPhotoDetail(f []string) string {
	id, title, filename, date := f[0], f[1], f[2], f[3]
	favorite, width, height, size := f[4], f[5], f[6], f[7]
	altitude, lat, lon, description, keywords := f[8], f[9], f[10], f[11], f[12]

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", orUntitled(title))
	fmt.Fprintf(&b, "  id: %s\n", id)
	fmt.Fprintf(&b, "  filename: %s\n", filename)
	fmt.Fprintf(&b, "  date: %s\n", orPlaceholder(date, "(unknown)"))
	fmt.Fprintf(&b, "  favorite: %s\n", favorite)
	fmt.Fprintf(&b, "  dimensions: %sx%s px\n", width, height)
	if size != "" {
		fmt.Fprintf(&b, "  size: %s\n", size)
	}
	if lat != "" && lon != "" {
		fmt.Fprintf(&b, "  location: %s, %s", lat, lon)
		if altitude != "" {
			fmt.Fprintf(&b, " (altitude %s m)", altitude)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("  location: (none)\n")
	}
	fmt.Fprintf(&b, "  description: %s\n", orPlaceholder(description, "(none)"))
	fmt.Fprintf(&b, "  keywords: %s\n", orPlaceholder(keywords, "(none)"))
	return compactOutput(b.String())
}

// orPlaceholder returns placeholder when s is empty/whitespace, else s.
func orPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}
	return s
}

// cappedPhotoLimit reads the "limit" parameter, applying the Photos default and
// the hard ceiling.
func cappedPhotoLimit(in map[string]any) int {
	limit, ok := getInt(in, "limit")
	if !ok || limit <= 0 {
		limit = defaultPhotoLimit
	}
	if limit > maxPhotoLimit {
		limit = maxPhotoLimit
	}
	return limit
}

// photosScriptError wraps an osascript failure with a hint about the most common
// cause — Automation access to Photos not yet granted — so the caller gets an
// actionable message instead of a bare AppleScript error number. Mirrors
// notesScriptError.
func photosScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: Photos automation failed (%s). If this is the first use, macOS may need you to grant this app access to Photos in System Settings → Privacy & Security → Automation", op, stderr)
}
