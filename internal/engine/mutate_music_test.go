// mutate_music_test.go tests the pure plan-assembly and script shape of the
// Music transport controls (play_pause, next_track, previous_track).
//
// SAFETY: nothing here runs a control. The stage functions probe the live Music
// app via osascript, so the tests exercise the pure `musicControlPlan` half and
// the fixed script constants directly — no playback is ever toggled and no
// Automation grant is needed.
package engine

import (
	"strings"
	"testing"
)

// TestMusicControlPlan_Shape checks the plan every control produces: an
// osascript forward command carrying the fixed script after the "--" terminator,
// a nil Inverse (irreversible), and a preview that does NOT repeat the server's
// "cannot be undone" suffix (the auto-commit path appends that itself).
func TestMusicControlPlan_Shape(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{"play_pause", playPauseScript},
		{"next_track", nextTrackScript},
		{"previous_track", previousTrackScript},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := musicControlPlan(tc.script, "do the thing. The change takes effect at once.")
			if plan.Inverse != nil {
				t.Errorf("%s is irreversible: Inverse should be nil", tc.name)
			}
			if plan.Forward.Binary != "osascript" {
				t.Errorf("forward binary = %q, want osascript", plan.Forward.Binary)
			}
			// argv must be exactly: -e <script> -- (no data operands for a fixed script).
			want := []string{"-e", tc.script, "--"}
			if len(plan.Forward.Args) != len(want) {
				t.Fatalf("forward argv = %q, want %q", plan.Forward.Args, want)
			}
			for i := range want {
				if plan.Forward.Args[i] != want[i] {
					t.Errorf("forward argv[%d] = %q, want %q", i, plan.Forward.Args[i], want[i])
				}
			}
			if strings.Contains(plan.Preview, "cannot be undone") {
				t.Errorf("preview must not repeat the server's 'cannot be undone' suffix, got %q", plan.Preview)
			}
		})
	}
}

// TestMusicControlScripts_Content pins the essentials of each fixed script: the
// correct Music transport verb AND the `is running` guard that prevents the
// forward command from relaunching Music if it quit between stage and commit.
func TestMusicControlScripts_Content(t *testing.T) {
	guard := `if not (application "Music" is running) then error`
	cases := map[string]string{
		"playpause":      playPauseScript,
		"next track":     nextTrackScript,
		"previous track": previousTrackScript,
	}
	for verb, script := range cases {
		if !strings.Contains(script, verb) {
			t.Errorf("script missing transport verb %q:\n%s", verb, script)
		}
		if !strings.Contains(script, guard) {
			t.Errorf("script for %q missing the never-launch running guard:\n%s", verb, script)
		}
	}
}

// TestMusicControls_AreMutators confirms each control is wired as a mutator (not
// a read-only builtin), so the server routes it through the staging/auto-commit
// path and never through Run.
func TestMusicControls_AreMutators(t *testing.T) {
	for _, name := range []string{"play_pause", "next_track", "previous_track"} {
		c := lookupCapability(t, name)
		if _, ok := lookupMutator(c.Builder); !ok {
			t.Errorf("%s should be a mutator, but %q is not registered", name, c.Builder)
		}
		if _, ok := lookupBuiltin(c.Builder); ok {
			t.Errorf("%s must not also be a builtin", name)
		}
		// All three are auto-commit + irreversible per the manifest.
		if !c.AutoCommit {
			t.Errorf("%s should be auto_commit", name)
		}
	}
}
