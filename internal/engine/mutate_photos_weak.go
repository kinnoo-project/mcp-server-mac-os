// mutate_photos_weak.go implements the Photos mutations that have NO scripted
// inverse — create_album, create_folder, add_to_album, and import_photos.
//
// # Why these carry no undo
//
// Photos' AppleScript dictionary exposes the forward action for each of these but
// not its reversal: there is no scripted way to remove a media item from an album
// or to delete an imported media item (the `delete` verb is restricted to
// albums/folders, and album/folder deletion is intentionally NOT offered by this
// server — see the project plan). So each stages with Inverse == nil; the server
// still parks them behind the stage→execute confirmation gate (they change the
// library), commits the forward command on approval, and tells the user plainly
// that the change cannot be undone automatically, including the manual reversal
// step. This keeps the "what executes is exactly what was previewed" guarantee
// while being honest about the missing rollback.
//
// As with every osascript mutator, all model values cross into AppleScript as
// "--"-terminated `on run argv` data, so a flag-like value can never be parsed as
// an osascript option.
package engine

import (
	"context"
	"fmt"
	"os"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// createAlbumScript makes a new album, at the top level or inside a named folder.
// argv: albumName, parentFolder ("" for top level).
const createAlbumScript = `on run argv
	set albName to item 1 of argv
	set parentName to item 2 of argv
	tell application "Photos"
		if parentName is "" then
			make new album named albName
		else
			make new album named albName at (first folder whose name is parentName)
		end if
	end tell
end run`

// createFolderScript makes a new folder, at the top level or inside a named
// parent folder. argv: folderName, parentFolder ("" for top level).
const createFolderScript = `on run argv
	set fName to item 1 of argv
	set parentName to item 2 of argv
	tell application "Photos"
		if parentName is "" then
			make new folder named fName
		else
			make new folder named fName at (first folder whose name is parentName)
		end if
	end tell
end run`

// addToAlbumScript adds existing media items (looked up by id) to an existing
// album. argv: albumName, id... A missing album or id makes the lookup error.
const addToAlbumScript = `on run argv
	set albName to item 1 of argv
	tell application "Photos"
		set theAlbum to first album whose name is albName
		set theItems to {}
		repeat with i from 2 to (count of argv)
			set end of theItems to (first media item whose id is (item i of argv))
		end repeat
		add theItems to theAlbum
	end tell
end run`

// importPhotosScript imports files into the library, optionally adding them to a
// named album. argv: albumName ("" for none), filePath...
const importPhotosScript = `on run argv
	set albName to item 1 of argv
	set theFiles to {}
	repeat with i from 2 to (count of argv)
		set end of theFiles to (POSIX file (item i of argv))
	end repeat
	tell application "Photos"
		if albName is "" then
			import theFiles
		else
			import theFiles into (first album whose name is albName)
		end if
	end tell
end run`

// stageCreateAlbum stages an album creation with no automatic undo.
func stageCreateAlbum(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	name, _ := getString(in, "name")
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("create_album: 'name' is required")
	}
	parent, _ := getString(in, "parent_folder")
	return &StagedPlan{
		Preview: fmt.Sprintf("Create album %q%s.\n\nThis cannot be undone automatically: Photos has no scripted album deletion. To reverse it, delete the album in Photos (your photos are unaffected).",
			name, inFolderClause(parent)),
		Forward: osascriptCommand(createAlbumScript, name, parent),
		Inverse: nil,
	}, nil
}

// stageCreateFolder stages a folder creation with no automatic undo.
func stageCreateFolder(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	name, _ := getString(in, "name")
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("create_folder: 'name' is required")
	}
	parent, _ := getString(in, "parent_folder")
	return &StagedPlan{
		Preview: fmt.Sprintf("Create folder %q%s.\n\nThis cannot be undone automatically: Photos has no scripted folder deletion. To reverse it, delete the folder in Photos.",
			name, inFolderClause(parent)),
		Forward: osascriptCommand(createFolderScript, name, parent),
		Inverse: nil,
	}, nil
}

// stageAddToAlbum stages adding existing items to an album with no automatic undo.
func stageAddToAlbum(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	album, _ := getString(in, "album")
	if strings.TrimSpace(album) == "" {
		return nil, fmt.Errorf("add_to_album: 'album' is required (see list_albums)")
	}
	ids, ok := getStringList(in, "ids")
	if !ok || len(ids) == 0 {
		return nil, fmt.Errorf("add_to_album: 'ids' is required (one or more media item ids)")
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("add_to_album: 'ids' must not contain an empty id")
		}
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Add %d item(s) to album %q.\n\nThis cannot be undone automatically: Photos has no scripted way to remove an item from an album. To reverse it, remove the items in Photos (only album membership changes; the photos are not copied or moved).",
			len(ids), album),
		Forward: osascriptCommand(addToAlbumScript, append([]string{album}, ids...)...),
		Inverse: nil,
	}, nil
}

// stageImportPhotos stages a file import with no automatic undo. Each file's
// existence is checked at stage time (a read-only stat) so a typo fails before
// commit rather than producing a confusing AppleScript error.
func stageImportPhotos(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	files, ok := getStringList(in, "files")
	if !ok || len(files) == 0 {
		return nil, fmt.Errorf("import_photos: 'files' is required (one or more file paths)")
	}
	for _, f := range files {
		if strings.TrimSpace(f) == "" {
			return nil, fmt.Errorf("import_photos: 'files' must not contain an empty path")
		}
		// Require a regular file: reject not just directories but also special
		// files (devices, sockets, fifos), matching how send_message validates its
		// attachments — Photos can only import an actual image/video file.
		if info, err := os.Stat(f); err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("import_photos: %q is not an existing regular file", f)
		}
	}
	album, _ := getString(in, "album")
	return &StagedPlan{
		Preview: fmt.Sprintf("Import %d file(s) into Photos%s.\n\nThis cannot be undone automatically: Photos has no scripted media-item deletion. To reverse it, delete the imported items in Photos (they then sit in Recently Deleted, recoverable for 30 days).",
			len(files), intoAlbumClause(album)),
		Forward: osascriptCommand(importPhotosScript, append([]string{album}, files...)...),
		Inverse: nil,
	}, nil
}

// inFolderClause renders the optional "in folder X" tail for a create preview.
func inFolderClause(parent string) string {
	if strings.TrimSpace(parent) == "" {
		return ""
	}
	return fmt.Sprintf(" in folder %q", parent)
}

// intoAlbumClause renders the optional "and add them to album X" tail for an
// import preview.
func intoAlbumClause(album string) string {
	if strings.TrimSpace(album) == "" {
		return ""
	}
	return fmt.Sprintf(" and add them to album %q", album)
}
