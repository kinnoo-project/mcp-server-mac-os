// builtins_music_test.go tests the pure, in-Go logic of the Music read builtin
// (now_playing) and the shared readiness-probe interpretation: rendering the
// player state + track into text, the not-running sentinel, and the mapping of a
// probe result to nil / not-running / permission errors.
//
// SAFETY: nothing here launches osascript or touches the real Music app — the
// tests feed the pure formatters the kind of output the scripts emit, so they
// read no live playback and require no Automation grant. The osascript
// "--"-terminator hardening is the SAME osascriptArgv seam exercised elsewhere;
// a dedicated case re-asserts it for the Music read script.
package engine

import (
	"strings"
	"testing"
)

// sep joins now_playing fields the way the AppleScript does (character id 31).
const musicSep = asFieldSep

// TestFormatNowPlaying_States covers each player state the script can report,
// plus the missing-artist/album omission and the not-running sentinel.
func TestFormatNowPlaying_States(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // substrings the rendered line must contain
		not  []string // substrings it must NOT contain
	}{
		{
			name: "playing full track",
			in:   "playing" + musicSep + "Song Title" + musicSep + "The Artist" + musicSep + "The Album",
			want: []string{"Playing", "Song Title", "The Artist", "The Album"},
		},
		{
			name: "paused shows Paused verb",
			in:   "paused" + musicSep + "Ballad" + musicSep + "Someone" + musicSep + "Record",
			want: []string{"Paused", "Ballad", "Someone"},
			not:  []string{"Playing"},
		},
		{
			name: "stopped has no track",
			in:   "stopped" + musicSep + musicSep + musicSep,
			want: []string{"stopped"},
			not:  []string{"Playing", "Paused"},
		},
		{
			name: "not running sentinel",
			in:   musicNotRunningSentinel,
			want: []string{"not running"},
		},
		{
			name: "missing artist and album are omitted, not shown empty",
			in:   "playing" + musicSep + "Just A Title" + musicSep + musicSep,
			want: []string{"Playing", "Just A Title"},
			not:  []string{" — ", "()"},
		},
		{
			name: "empty title falls back to placeholder",
			in:   "playing" + musicSep + musicSep + "Artist Only" + musicSep,
			want: []string{"(untitled)", "Artist Only"},
		},
		{
			name: "unknown future state word passes through",
			in:   "fast forwarding" + musicSep + "Track" + musicSep + "A" + musicSep + "B",
			want: []string{"Fast-forwarding", "Track"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatNowPlaying(tc.in)
			if err != nil {
				t.Fatalf("formatNowPlaying(%q): %v", tc.in, err)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("rendered %q missing %q", got, w)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("rendered %q should not contain %q", got, n)
				}
			}
		})
	}
}

// TestFormatNowPlaying_MalformedOutput verifies a probe line with the wrong field
// count is rejected as an error rather than silently mis-rendered.
func TestFormatNowPlaying_MalformedOutput(t *testing.T) {
	if _, err := formatNowPlaying("playing" + musicSep + "only two fields"); err == nil {
		t.Error("expected an error for a short (2-field) probe line, got nil")
	}
}

// TestInterpretMusicReady maps synthetic probe results to the three outcomes the
// mutators depend on: ready (nil), not-running (clear error), and a denied Apple
// event (a Music-automation hint) — all without launching osascript.
func TestInterpretMusicReady(t *testing.T) {
	if err := interpretMusicReady("play_pause", &runResult{Stdout: musicReadyMarker + "\n"}); err != nil {
		t.Errorf("READY should map to nil, got %v", err)
	}

	err := interpretMusicReady("play_pause", &runResult{Stdout: musicNotRunningSentinel + "\n"})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("NOTRUNNING should map to a 'not running' error, got %v", err)
	}

	// A denied Automation grant surfaces as a non-zero exit with stderr; the error
	// must point the user at the Automation privacy pane.
	err = interpretMusicReady("next_track", &runResult{ExitCode: 1, Stderr: "Not authorized to send Apple events to Music."})
	if err == nil || !strings.Contains(err.Error(), "Automation") {
		t.Errorf("a denied probe should map to a Music-automation hint, got %v", err)
	}

	if err := interpretMusicReady("play_pause", &runResult{Stdout: "surprise\n"}); err == nil {
		t.Error("an unexpected probe output should be an error")
	}
}

// TestMusicReadScript_UsesOptionTerminator documents the structural "data, never
// code" guarantee for the read path: even though now_playing takes no arguments,
// the argv it runs through still carries the "--" terminator, so the hardening is
// uniform with every other osascript builtin.
func TestMusicReadScript_UsesOptionTerminator(t *testing.T) {
	argv := osascriptArgv(nowPlayingScript)
	if indexOf(argv, "--") < 0 {
		t.Fatalf("now_playing argv missing '--' terminator: %v", argv)
	}
}

// TestRunNowPlaying_IsReadOnly is a lightweight guard that now_playing is wired
// as a read-only builtin (not a mutator), so the server routes it through Run and
// never through the staging path.
func TestRunNowPlaying_IsReadOnly(t *testing.T) {
	c := lookupCapability(t, "now_playing")
	if _, ok := lookupBuiltin(c.Builder); !ok {
		t.Errorf("now_playing should be a builtin, but %q is not registered", c.Builder)
	}
	if _, ok := lookupMutator(c.Builder); ok {
		t.Errorf("now_playing must not also be a mutator")
	}
}
