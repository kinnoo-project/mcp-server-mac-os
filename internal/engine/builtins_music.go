// builtins_music.go implements the read half of the `application-music` domain —
// now_playing — plus the shared "is Music ready to control?" probe that the
// mutating controls in mutate_music.go depend on. Both drive Music.app through
// its AppleScript dictionary via the hardened osascript seam in applescript.go.
//
// # Why AppleScript
//
// Music.app exposes its player state and the current track (title/artist/album)
// only through its scripting dictionary; there is no CLI that reads them. So —
// like the Mail, Notes, Calendar, and Safari reads — the work is a FIXED
// AppleScript program plus result parsing, and lives here as an in-process
// builtin rather than a generic argv builder.
//
// # Never launch Music just to answer
//
// A bare `tell application "Music"` would LAUNCH Music if it were not already
// running, which is a surprising side effect for a read (and for a "skip track"
// the user only meant to apply to music already playing). Every script here first
// gates on `application "Music" is running` — a LaunchServices query that does
// NOT start the app and needs no Automation grant — and treats a not-running
// Music as a clean, reported state rather than an occasion to open it.
//
// # Guardrails
//
//   - No operation in this domain takes a free-text parameter: now_playing and
//     the three controls are all zero-parameter. There is therefore no
//     model-supplied value that could carry an injection payload, so no
//     dash-leading value to neutralize and no reviewedFreeTextBuiltins entry is
//     required. The osascript "--" terminator is still inserted structurally by
//     the shared seam (applescript.go).
//   - Each track field is flattened with _clean so a title/artist/album
//     containing a tab or newline cannot corrupt the delimiter-joined output the
//     Go parser splits on; a missing field coerces to "" via _clean rather than
//     erroring.
//   - A denied Automation permission (the first control of Music prompts for it)
//     surfaces as an actionable hint via musicScriptError rather than a bare
//     AppleScript error number. Music is a NEW Automation permission surface — no
//     other capability drives it.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// musicNotRunningSentinel is the exact stdout a script returns when Music is not
// running. It is a fixed marker (not localizable app text) so the Go side can
// distinguish "Music is closed" — a benign state to report plainly — from a real
// failure, without depending on Music being launched to answer.
const musicNotRunningSentinel = "NOTRUNNING"

// musicReadyMarker is the stdout the readiness probe returns when Music is
// running AND an Apple event to it succeeded (so Automation access is granted).
const musicReadyMarker = "READY"

// nowPlayingScript returns the current playback state and track, joined by
// asFieldSep (0x1f) so the four fields split cleanly even though a title, artist,
// or album can contain arbitrary characters:
//
//	state \x1f name \x1f artist \x1f album
//
// state is Music's `player state` coerced to text ("playing", "paused",
// "stopped", "fast forwarding", "rewinding"). When stopped there is no current
// track — reading `current track` would error — so that case returns the state
// word with three empty fields. When Music is not running the script returns the
// NOTRUNNING sentinel WITHOUT sending any Apple event (so Music is never
// launched). Helper calls are prefixed with `my` so they resolve to this
// script's _clean handler rather than Music's dictionary.
const nowPlayingScript = asDateHelpers + `on run argv
	set sep to (character id 31)
	if not (application "Music" is running) then return "NOTRUNNING"
	tell application "Music"
		set stateVal to (player state as string)
		if stateVal is "stopped" then return stateVal & sep & "" & sep & "" & sep & ""
		set t to current track
		set tName to my _clean(name of t)
		set tArtist to my _clean(artist of t)
		set tAlbum to my _clean(album of t)
	end tell
	return stateVal & sep & tName & sep & tArtist & sep & tAlbum
end run`

// musicReadyProbeScript is the shared readiness check the mutating controls run
// at stage time. It returns:
//
//   - "NOTRUNNING" when Music is not running (no Apple event sent, Music not
//     launched) — so a control fails cleanly instead of committing a no-op or
//     launching the app;
//   - "READY" when Music is running AND a probe Apple event (`get player state`)
//     succeeds — which is what also confirms Automation access is granted;
//   - a non-zero exit with stderr when that Apple event is denied (Automation not
//     granted) or otherwise fails, so the mutator can surface an actionable hint
//     BEFORE it commits anything.
//
// Sending a real Apple event (rather than only checking `is running`) is
// deliberate: it makes a denied-Automation failure surface at stage time — where
// the two-phase machinery reports it as a clean error — instead of at commit
// time, where a non-zero exit from the fire-and-forget control would be
// misreported as "Done".
const musicReadyProbeScript = `on run argv
	if not (application "Music" is running) then return "NOTRUNNING"
	tell application "Music" to get player state
	return "READY"
end run`

// runNowPlaying reports what Music is currently playing (state + track), or that
// Music is not running. Read-only; it never launches Music.
func runNowPlaying(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	res, err := runOsascript(ctx, nowPlayingScript)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", musicScriptError("now_playing", res.Stderr)
	}
	// osascript prints one trailing newline after the returned string; strip
	// exactly that one before comparing/splitting.
	return formatNowPlaying(strings.TrimSuffix(res.Stdout, "\n"))
}

// formatNowPlaying turns the now_playing script's raw stdout into human text. It
// is the pure half of runNowPlaying — split out so the sentinel handling, field
// splitting, and rendering can be unit-tested without launching osascript or
// touching the live Music app.
func formatNowPlaying(out string) (string, error) {
	if out == musicNotRunningSentinel {
		return "Music is not running, so nothing is playing.", nil
	}
	// state <sep> name <sep> artist <sep> album; SplitN with n=4 keeps any field
	// intact (a title never contains the 0x1f separator).
	parts := strings.SplitN(out, asFieldSep, 4)
	if len(parts) != 4 {
		return "", fmt.Errorf("now_playing: unexpected probe output")
	}
	return renderNowPlaying(parts[0], parts[1], parts[2], parts[3]), nil
}

// renderNowPlaying formats the parsed player state and track into one
// human-readable line. A "stopped" state has no track to show; every other state
// renders "<Verb>: <title> — <artist> (<album>)", omitting the artist/album
// clauses when Music reported them empty.
func renderNowPlaying(state, name, artist, album string) string {
	if state == "stopped" {
		return "Music is open but stopped — nothing is playing."
	}
	// Map Music's lower-case state words to a readable verb; an unrecognised state
	// (future Music release) falls back to the raw word rather than mislabeling it.
	verb := map[string]string{
		"playing":         "Playing",
		"paused":          "Paused",
		"fast forwarding": "Fast-forwarding",
		"rewinding":       "Rewinding",
	}[state]
	if verb == "" {
		verb = state
	}
	line := fmt.Sprintf("%s: %s", verb, orUntitled(name))
	if artist != "" {
		line += " — " + artist
	}
	if album != "" {
		line += fmt.Sprintf(" (%s)", album)
	}
	return line
}

// ensureMusicReady runs the shared readiness probe and translates its result into
// a nil error (Music running + Automation granted) or a clear, actionable error:
// "not running" when Music is closed, or a musicScriptError hint when the probe
// Apple event was denied/failed. Both mutating and (potentially) future reading
// callers use it so the not-running and permission cases are handled once.
func ensureMusicReady(ctx context.Context, op string) error {
	res, err := runOsascript(ctx, musicReadyProbeScript)
	if err != nil {
		return err
	}
	return interpretMusicReady(op, res)
}

// interpretMusicReady is the pure half of ensureMusicReady: it maps a probe's
// run result to nil / not-running / permission errors, split out so those
// branches are unit-tested from synthetic results without launching osascript.
func interpretMusicReady(op string, res *runResult) error {
	if res.ExitCode != 0 {
		return musicScriptError(op, res.Stderr)
	}
	switch strings.TrimSuffix(res.Stdout, "\n") {
	case musicReadyMarker:
		return nil
	case musicNotRunningSentinel:
		return fmt.Errorf("%s: Music is not running — open the Music app and start playback first (it is not launched automatically)", op)
	default:
		return fmt.Errorf("%s: unexpected readiness-probe output", op)
	}
}

// musicScriptError wraps an osascript failure with a hint about the most common
// cause — Automation access to Music not yet granted — so the caller gets an
// actionable message instead of a bare AppleScript error number. Mirrors
// safariScriptError/mailScriptError; Music is a new Automation permission surface
// (no other capability drives it).
func musicScriptError(op, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		stderr = "osascript reported a non-zero exit with no detail"
	}
	return fmt.Errorf("%s: Music automation failed (%s). If this is the first use, macOS may need you to grant this app access to Music in System Settings → Privacy & Security → Automation", op, stderr)
}
