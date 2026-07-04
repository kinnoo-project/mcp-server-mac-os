// applescript_compile_test.go is a registry-driven invariant, in the same spirit
// as injection_sweep_test.go: it walks every embedded AppleScript source
// constant in this package and proves each one actually COMPILES.
//
// This exists because of a real, shipped bug: builtins_contacts.go's
// getContactAddrHelper and builtins_music.go's nowPlayingScript both declared a
// bare local variable named `st` — which collides with a reserved AppleScript
// unit-abbreviation token (the weight unit "stone"), so `set st to ...` fails to
// compile on every Mac. Neither Go's compiler nor any existing unit test could
// have caught this: the scripts are plain Go string constants from Go's point of
// view, so nothing checks that the embedded AppleScript text is even syntactically
// valid until osascript tries to run it live — which is exactly what happened,
// silently, in production. See docs/issues/bug-st-reserved-word-breaks-get-contact-and-now-playing.md.
//
// osacompile -e <script> -o <path> is the right tool for this: it COMPILES the
// source into a .scpt file without ever running it, so a `tell application "X"`
// block never actually contacts X and no Apple Event is ever sent — this test
// needs no Automation grants and touches no live application state. AppleScript
// also resolves `my <handler>(...)` calls at RUN time, not compile time, so a
// helper fragment (e.g. asDateHelpers alone, with no `on run`) compiles cleanly
// on its own even though its handlers are meant to be prepended to another
// script — there is no need to reassemble the full concatenated script here,
// only to check each constant in isolation.
package engine

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// allEmbeddedAppleScripts lists every top-level AppleScript source constant in
// this package by name, so TestAllEmbeddedAppleScriptsCompile can prove each one
// is syntactically valid. A newly added script constant that is not added here
// simply isn't covered — there is no way to enumerate Go string constants via
// reflection, so this list must be maintained by hand alongside new scripts.
var allEmbeddedAppleScripts = map[string]string{
	"asDateHelpers":               asDateHelpers,
	"listRunningAppsScript":       listRunningAppsScript,
	"listCalendarsScript":         listCalendarsScript,
	"queryEventsScript":           queryEventsScript,
	"listInboxScript":             listInboxScript,
	"readMessageScript":           readMessageScript,
	"getContactAddrHelper":        getContactAddrHelper,
	"getContactScript":            getContactScript,
	"nowPlayingScript":            nowPlayingScript,
	"musicReadyProbeScript":       musicReadyProbeScript,
	"listNotesScript":             listNotesScript,
	"searchNotesScript":           searchNotesScript,
	"readNoteScript":              readNoteScript,
	"listFoldersScript":           listFoldersScript,
	"findContactScript":           findContactScript,
	"exportPhotoScript":           exportPhotoScript,
	"photoRowHelpers":             photoRowHelpers,
	"listPhotosScript":            listPhotosScript,
	"searchPhotosScript":          searchPhotosScript,
	"albumPhotosScript":           albumPhotosScript,
	"favoritesScript":             favoritesScript,
	"recentlyDeletedScript":       recentlyDeletedScript,
	"selectionScript":             selectionScript,
	"listAlbumsScript":            listAlbumsScript,
	"photoFoldersScript":          photoFoldersScript,
	"statsScript":                 statsScript,
	"getPhotoScript":              getPhotoScript,
	"listRemindersScript":         listRemindersScript,
	"listTabsScript":              listTabsScript,
	"currentTabScript":            currentTabScript,
	"listWindowsScript":           listWindowsScript,
	"addEventScript":              addEventScript,
	"deleteEventByKeyScript":      deleteEventByKeyScript,
	"deleteEventByUidScript":      deleteEventByUidScript,
	"modifyEventScript":           modifyEventScript,
	"probeEventScript":            probeEventScript,
	"activateScript":              activateScript,
	"quitScript":                  quitScript,
	"createContactScript":         createContactScript,
	"deleteContactByMarkerScript": deleteContactByMarkerScript,
	"sendMailAppleScript":         sendMailAppleScript,
	"playPauseScript":             playPauseScript,
	"nextTrackScript":             nextTrackScript,
	"previousTrackScript":         previousTrackScript,
	"sendIMessageScript":          sendIMessageScript,
	"probePhotoScript":            probePhotoScript,
	"probeKeywordsScript":         probeKeywordsScript,
	"setFavoriteScript":           setFavoriteScript,
	"setNameScript":               setNameScript,
	"setDescriptionScript":        setDescriptionScript,
	"setDateScript":               setDateScript,
	"setKeywordsScript":           setKeywordsScript,
	"createAlbumScript":           createAlbumScript,
	"createFolderScript":          createFolderScript,
	"addToAlbumScript":            addToAlbumScript,
	"importPhotosScript":          importPhotosScript,
	"createNoteScript":            createNoteScript,
	"deleteNoteByTitleScript":     deleteNoteByTitleScript,
	"probeNoteBodyScript":         probeNoteBodyScript,
	"setNoteBodyScript":           setNoteBodyScript,
	"readDarkModeScript":          readDarkModeScript,
	"setDarkModeScript":           setDarkModeScript,
	"createReminderScript":        createReminderScript,
	"deleteReminderByKeyScript":   deleteReminderByKeyScript,
	"deleteReminderByIdScript":    deleteReminderByIdScript,
	"modifyReminderScript":        modifyReminderScript,
	"completeReminderScript":      completeReminderScript,
	"probeReminderScript":         probeReminderScript,
	"notifyAppleScript":           notifyAppleScript,
	"setWindowPositionScript":     setWindowPositionScript,
	"setWindowSizeScript":         setWindowSizeScript,
	"setWindowMinimizedScript":    setWindowMinimizedScript,
	"probeWindowGeometryScript":   probeWindowGeometryScript,
	"probeWindowMinimizedScript":  probeWindowMinimizedScript,
}

// TestAllEmbeddedAppleScriptsCompile proves every script in allEmbeddedAppleScripts
// is syntactically valid AppleScript by compiling it with osacompile (which never
// executes the script, so no Apple Event is sent and no Automation grant is
// needed). A bare reserved-word collision like the `st`/"stone" bug this test was
// added for fails here immediately, instead of silently at live runtime.
func TestAllEmbeddedAppleScriptsCompile(t *testing.T) {
	osacompile, err := exec.LookPath("osacompile")
	if err != nil {
		t.Skip("osacompile not found on PATH; skipping AppleScript compile check")
	}

	for name, src := range allEmbeddedAppleScripts {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "check.scpt")
			cmd := exec.Command(osacompile, "-e", src, "-o", out)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Errorf("%s failed to compile: %v\n%s", name, err, stderr.String())
			}
			_ = os.Remove(out)
		})
	}
}
