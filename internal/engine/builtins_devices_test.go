// builtins_devices_test.go exercises the pure parsing/formatting helpers behind
// the AirPlay and input-source reads against synthetic command output — no live
// dns-sd browse or defaults read is performed. Neither capability takes a
// model-controlled parameter, so there is no injection regression to assert here
// (nothing but constants ever reaches an argv); these tests instead pin the
// output parsing, which is where the real complexity lives.
package engine

import (
	"strings"
	"testing"
)

// sampleAirplayBrowse mirrors real `dns-sd -B _airplay._tcp` output: a banner,
// a header row, then Add rows (one receiver advertised on two interfaces, plus a
// second receiver whose name contains a space and a quote).
const sampleAirplayBrowse = `Browsing for _airplay._tcp
DATE: ---Thu 02 Jul 2026---
15:50:32.792  ...STARTING...
Timestamp     A/R    Flags  if Domain               Service Type         Instance Name
15:50:32.792  Add        3   1 local.               _airplay._tcp.       Living Room Apple TV
15:50:32.792  Add        3  11 local.               _airplay._tcp.       Living Room Apple TV
15:50:32.792  Add        2  11 local.               _airplay._tcp.       55" The Frame`

func TestParseAirplayDevices(t *testing.T) {
	got := parseAirplayDevices(sampleAirplayBrowse)
	want := []string{"Living Room Apple TV", `55" The Frame`}
	if len(got) != len(want) {
		t.Fatalf("parseAirplayDevices = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("device[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A device that announces (Add) then withdraws (Rmv) inside the browse window
// must not be reported; the header/status lines must never be mistaken for a
// device.
func TestParseAirplayDevices_AddThenRemove(t *testing.T) {
	out := `Timestamp     A/R    Flags  if Domain               Service Type         Instance Name
10:00:00.000  Add        3   1 local.               _airplay._tcp.       Flaky Speaker
10:00:01.000  Add        3   1 local.               _airplay._tcp.       Steady TV
10:00:02.000  Rmv        3   1 local.               _airplay._tcp.       Flaky Speaker`
	got := parseAirplayDevices(out)
	if len(got) != 1 || got[0] != "Steady TV" {
		t.Errorf("parseAirplayDevices = %v, want [Steady TV] (withdrawn device dropped)", got)
	}
}

func TestParseAirplayDevices_Empty(t *testing.T) {
	// Only the banner + header, no receivers.
	out := "Browsing for _airplay._tcp\nTimestamp     A/R    Flags  if Domain               Service Type         Instance Name\n"
	if got := parseAirplayDevices(out); len(got) != 0 {
		t.Errorf("parseAirplayDevices = %v, want empty", got)
	}
}

func TestRenderAirplayDevices(t *testing.T) {
	out := renderAirplayDevices([]string{"Living Room Apple TV", `55" The Frame`})
	for _, want := range []string{"2 AirPlay receiver(s)", "Living Room Apple TV", `55" The Frame`, "open_settings", "displays"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderAirplayDevices missing %q in:\n%s", want, out)
		}
	}
	empty := renderAirplayDevices(nil)
	if !strings.Contains(empty, "No AirPlay receivers") || !strings.Contains(empty, "Local Network") {
		t.Errorf("empty render should explain no receivers + Local Network, got:\n%s", empty)
	}
}

// sampleEnabledSources is a trimmed real AppleEnabledInputSources payload: a
// U.S. keyboard layout, two helper input methods (which must be filtered out),
// and two input modes (Pinyin, ITABC).
const sampleEnabledSources = `(
        {
        InputSourceKind = "Keyboard Layout";
        "KeyboardLayout ID" = 0;
        "KeyboardLayout Name" = "U.S.";
    },
        {
        "Bundle ID" = "com.apple.CharacterPaletteIM";
        InputSourceKind = "Non Keyboard Input Method";
    },
        {
        "Bundle ID" = "com.apple.inputmethod.TCIM";
        "Input Mode" = "com.apple.inputmethod.TCIM.Pinyin";
        InputSourceKind = "Input Mode";
    },
        {
        "Bundle ID" = "com.apple.inputmethod.SCIM";
        "Input Mode" = "com.apple.inputmethod.SCIM.ITABC";
        InputSourceKind = "Input Mode";
    }
)`

// sampleSelectedSources marks the U.S. layout as the active one (alongside a
// press-and-hold helper, which carries no user-selectable layout).
const sampleSelectedSources = `(
        {
        "Bundle ID" = "com.apple.PressAndHold";
        InputSourceKind = "Non Keyboard Input Method";
    },
        {
        InputSourceKind = "Keyboard Layout";
        "KeyboardLayout ID" = 0;
        "KeyboardLayout Name" = "U.S.";
    }
)`

func TestRenderInputSources(t *testing.T) {
	out := renderInputSources(sampleEnabledSources, sampleSelectedSources)

	// The three typing sources appear; the two helper input methods do not.
	for _, want := range []string{"U.S.", "Pinyin", "ITABC", "keyboard layout", "input method", "open_settings", "keyboard"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderInputSources missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "CharacterPalette") || strings.Contains(out, "PressAndHold") {
		t.Errorf("helper input methods should be filtered out, got:\n%s", out)
	}
	if !strings.Contains(out, "3 input source(s) enabled") {
		t.Errorf("expected 3 enabled sources, got:\n%s", out)
	}

	// The U.S. layout is the selected one, so it (and only it) is marked current.
	usLine := lineContaining(out, "U.S.")
	if !strings.Contains(usLine, "current") {
		t.Errorf("U.S. layout should be marked current, got line: %q", usLine)
	}
	if pinyin := lineContaining(out, "Pinyin"); strings.Contains(pinyin, "current") {
		t.Errorf("Pinyin should NOT be marked current, got line: %q", pinyin)
	}
}

// When the selected read is unavailable, the sources still list — just without a
// current marker.
func TestRenderInputSources_NoSelected(t *testing.T) {
	out := renderInputSources(sampleEnabledSources, "")
	if !strings.Contains(out, "U.S.") {
		t.Errorf("expected sources listed even without selected data, got:\n%s", out)
	}
	if strings.Contains(out, "current") {
		t.Errorf("no source should be marked current when selected data is absent, got:\n%s", out)
	}
}

func TestRenderInputSources_Unparseable(t *testing.T) {
	out := renderInputSources("not a plist at all", "")
	if !strings.Contains(out, "not a plist at all") {
		t.Errorf("unparseable input should fall back to raw data, got:\n%s", out)
	}
}

func TestParsePlistDicts(t *testing.T) {
	dicts := parsePlistDicts(sampleEnabledSources)
	if len(dicts) != 4 {
		t.Fatalf("parsePlistDicts got %d dicts, want 4", len(dicts))
	}
	if dicts[0]["KeyboardLayout Name"] != "U.S." {
		t.Errorf("first dict KeyboardLayout Name = %q, want U.S.", dicts[0]["KeyboardLayout Name"])
	}
	if dicts[0]["KeyboardLayout ID"] != "0" {
		t.Errorf("unquoted numeric value = %q, want 0", dicts[0]["KeyboardLayout ID"])
	}
	if dicts[2]["Input Mode"] != "com.apple.inputmethod.TCIM.Pinyin" {
		t.Errorf("Input Mode = %q, want com.apple.inputmethod.TCIM.Pinyin", dicts[2]["Input Mode"])
	}
}

func TestLastDotComponent(t *testing.T) {
	cases := map[string]string{
		"com.apple.inputmethod.SCIM.ITABC":  "ITABC",
		"com.apple.inputmethod.TCIM.Pinyin": "Pinyin",
		"nodots":                            "nodots",
		"trailing.":                         "trailing.", // no component after the dot: return unchanged
	}
	for in, want := range cases {
		if got := lastDotComponent(in); got != want {
			t.Errorf("lastDotComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

// lineContaining returns the first line of text that contains substr, or "".
func lineContaining(text, substr string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}
