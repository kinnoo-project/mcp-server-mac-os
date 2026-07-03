// mutate_music.go implements the `application-music` domain's three transport
// controls — play_pause, next_track, previous_track — that drive Music.app's
// playback through its AppleScript dictionary.
//
// # Why auto-commit and irreversible
//
// Each control is a single, immediate transport action a user would expect to
// take effect the instant they ask ("skip this", "pause"). Forcing a
// stage→execute confirmation round-trip on "next track" would be pure friction,
// so these run in the AUTO-COMMIT lane. They are also modeled as IRREVERSIBLE:
// pausing/skipping is its own manual compensation (press play again, or
// previous_track), and pretending to offer an "undo" that just re-toggles state
// would be misleading — so each plan carries a nil Inverse, and the auto-commit
// path renders the honest "cannot be undone" suffix from that.
//
// # Fail cleanly, never launch
//
// Unlike a bare `tell application "Music"`, these must not LAUNCH Music when it
// is closed — the user asked to control music that is (presumably) already
// playing. Each mutator therefore probes readiness at STAGE time via
// ensureMusicReady (see builtins_music.go): if Music is not running, staging
// fails with a clear message and nothing runs; if Music is running but Automation
// access is denied, that surfaces here too (the probe sends a real Apple event),
// so the denial is reported cleanly before commit rather than as a confusing
// "Done" over a silently-failed control. The forward script re-checks `is
// running` as a belt-and-braces guard against the rare case where Music quits
// between stage and commit, erroring instead of relaunching.
//
// # Injection
//
// Every script here is a FIXED Go constant with ZERO model-supplied arguments, so
// there is no data value to harden — but the shared osascript seam still inserts
// the "--" end-of-options terminator (osascriptCommand), keeping the hardening
// structural and uniform with the rest of the codebase.
package engine

import (
	"context"

	"mcp-server-mac-os/internal/registry"
)

// playPauseScript toggles playback. The `is running` guard errors (rather than
// relaunching Music) in the rare event Music quit between stage and commit.
const playPauseScript = `on run argv
	if not (application "Music" is running) then error "Music is not running"
	tell application "Music" to playpause
end run`

// nextTrackScript advances to the next track in the current queue.
const nextTrackScript = `on run argv
	if not (application "Music" is running) then error "Music is not running"
	tell application "Music" to next track
end run`

// previousTrackScript returns to the previous track (or restarts the current one,
// matching Music's own behaviour) in the current queue.
const previousTrackScript = `on run argv
	if not (application "Music" is running) then error "Music is not running"
	tell application "Music" to previous track
end run`

// stageMusicControl is the shared staging path for the transport controls: it
// confirms Music is running and controllable, then returns an auto-commit,
// irreversible plan whose forward command runs the given fixed script. A nil
// Inverse is intentional (see the file header).
func stageMusicControl(ctx context.Context, op, script, preview string) (*StagedPlan, error) {
	if err := ensureMusicReady(ctx, op); err != nil {
		return nil, err
	}
	return musicControlPlan(script, preview), nil
}

// musicControlPlan assembles the auto-commit, irreversible plan for a transport
// control. It is the pure half of stageMusicControl — no probing, no side effects
// — split out so the plan's shape (osascript forward through the "--" seam, nil
// Inverse) is unit-tested without launching osascript or the live Music app.
func musicControlPlan(script, preview string) *StagedPlan {
	return &StagedPlan{
		Preview: preview,
		Forward: osascriptCommand(script),
		Inverse: nil, // irreversible: play/pause/skip is its own manual reversal
	}
}

// stagePlayPause stages (for immediate auto-commit) toggling Music playback.
func stagePlayPause(ctx context.Context, _ registry.Capability, _ map[string]any) (*StagedPlan, error) {
	return stageMusicControl(ctx, "play_pause", playPauseScript,
		"Toggle Music playback: start playing if paused or stopped, or pause if playing. The change takes effect at once.")
}

// stageNextTrack stages (for immediate auto-commit) skipping to the next track.
func stageNextTrack(ctx context.Context, _ registry.Capability, _ map[string]any) (*StagedPlan, error) {
	return stageMusicControl(ctx, "next_track", nextTrackScript,
		"Skip Music to the next track in the current playback queue. The change takes effect at once.")
}

// stagePreviousTrack stages (for immediate auto-commit) going back a track.
func stagePreviousTrack(ctx context.Context, _ registry.Capability, _ map[string]any) (*StagedPlan, error) {
	return stageMusicControl(ctx, "previous_track", previousTrackScript,
		"Return Music to the previous track (or restart the current one) in the current playback queue. The change takes effect at once.")
}
