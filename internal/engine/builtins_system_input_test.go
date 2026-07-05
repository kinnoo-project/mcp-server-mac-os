// builtins_system_input_test.go covers the V9 read-only system probes: the
// hidutil UserKeyMapping parser (including the empty forms), the round trip
// between a curated remap's JSON and the text hidutil reports back, the human
// rendering of key_remap_status, and the loopback port probe behind
// sharing_status.
package engine

import (
	"context"
	"net"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestParseUserKeyMapping_Empty confirms both empty forms hidutil emits — the
// literal "(null)" and an empty "()" array — parse to zero pairs, so a Mac at its
// keyboard defaults reads as "no remapping" (and remap_key's inverse for that
// state clears the mapping rather than restoring a phantom pair).
func TestParseUserKeyMapping_Empty(t *testing.T) {
	for _, in := range []string{"(null)\n", "(\n)\n", "", "(\n)"} {
		if got := parseUserKeyMapping(in); len(got) != 0 {
			t.Errorf("parseUserKeyMapping(%q) = %+v, want no pairs", in, got)
		}
	}
}

// TestParseUserKeyMapping_Pairs parses a two-entry mapping in hidutil's real
// --get output shape (old-style plist, decimal values, Dst listed before Src) and
// checks both pairs come back with the right source and destination.
func TestParseUserKeyMapping_Pairs(t *testing.T) {
	// Caps Lock → Escape, and Left Command → Left Option (two dicts).
	out := `(
        {
        HIDKeyboardModifierMappingDst = 30064771113;
        HIDKeyboardModifierMappingSrc = 30064771129;
    }
        {
        HIDKeyboardModifierMappingDst = 30064771298;
        HIDKeyboardModifierMappingSrc = 30064771299;
    }
)`
	pairs := parseUserKeyMapping(out)
	if len(pairs) != 2 {
		t.Fatalf("parsed %d pairs, want 2: %+v", len(pairs), pairs)
	}
	if pairs[0].src != hidCapsLock || pairs[0].dst != hidEscape {
		t.Errorf("pair 0 = %+v, want Caps Lock→Escape", pairs[0])
	}
	if pairs[1].src != hidLeftGUI || pairs[1].dst != hidLeftAlt {
		t.Errorf("pair 1 = %+v, want Left Command→Left Option", pairs[1])
	}
}

// TestMatchCuratedRemap_OrderInsensitive verifies a parsed mapping is recognized
// as a curated remap even when hidutil reports its pairs in a different order than
// we set them (hidutil does not preserve order), so key_remap_status can name a
// swap remap rather than dumping raw pairs.
func TestMatchCuratedRemap_OrderInsensitive(t *testing.T) {
	// swap_command_option's four pairs, reversed.
	want := curatedRemaps["swap_command_option"]
	shuffled := make([]keyPair, len(want))
	for i := range want {
		shuffled[i] = want[len(want)-1-i]
	}
	if name := matchCuratedRemap(shuffled); name != "swap_command_option" {
		t.Errorf("matchCuratedRemap(shuffled swap) = %q, want swap_command_option", name)
	}
	// A mapping that is not curated returns "".
	if name := matchCuratedRemap([]keyPair{{hidEscape, hidCapsLock}}); name != "" {
		t.Errorf("matchCuratedRemap(non-curated) = %q, want empty", name)
	}
}

// TestRunKeyRemapStatus_Rendering checks the two human-facing shapes of the
// status output by parsing then rendering directly (the live path only differs by
// where the text comes from): a curated remap is NAMED, an empty mapping reports
// defaults.
func TestRunKeyRemapStatus_Rendering(t *testing.T) {
	// Named-remap rendering: describePriorMapping labels a curated match.
	capsEsc := curatedRemaps["caps_lock_to_escape"]
	if desc := describePriorMapping(capsEsc); !strings.Contains(desc, "caps_lock_to_escape") ||
		!strings.Contains(desc, "Caps Lock → Escape") {
		t.Errorf("describePriorMapping(caps→esc) = %q, want it to name the remap and the arrow", desc)
	}
	// Empty rendering.
	if desc := describePriorMapping(nil); !strings.Contains(desc, "no custom remapping") {
		t.Errorf("describePriorMapping(nil) = %q, want a 'no custom remapping' phrase", desc)
	}
}

// TestHidUsageName_KnownAndUnknown confirms known modifier codes render as human
// labels and an unknown code falls back to hex (so a user's own custom mapping
// still displays intelligibly).
func TestHidUsageName_KnownAndUnknown(t *testing.T) {
	if got := hidUsageName(hidCapsLock); got != "Caps Lock" {
		t.Errorf("hidUsageName(capsLock) = %q, want Caps Lock", got)
	}
	if got := hidUsageName(0x700000004); got != "0x700000004" {
		t.Errorf("hidUsageName(unknown) = %q, want hex fallback", got)
	}
}

// TestProbeLoopbackPort detects on/off correctly: a port we OWN a listener on
// reads as on, and a port with nothing listening reads as off. Binding an
// ephemeral listener gives a deterministic "on" case with no dependency on any
// real sharing service being enabled.
func TestProbeLoopbackPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to open a test listener: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if !probeLoopbackPort(context.Background(), port) {
		t.Errorf("probeLoopbackPort(%d) = false with a live listener, want true", port)
	}

	// Close the listener, then the same port should read as off (nothing bound).
	ln.Close()
	if probeLoopbackPort(context.Background(), port) {
		t.Errorf("probeLoopbackPort(%d) = true after closing the listener, want false", port)
	}
}

// TestRunSharingStatus_Shape confirms sharing_status reports all three services
// with an explicit ON/OFF verdict and points at the settings hand-off. It does
// not assert which state each service is in (that depends on the host machine) —
// only that the report is well-formed and complete.
func TestRunSharingStatus_Shape(t *testing.T) {
	out, err := runSharingStatus(context.Background(), registry.Capability{}, nil)
	if err != nil {
		t.Fatalf("runSharingStatus: %v", err)
	}
	for _, name := range []string{"Remote Login (SSH)", "Screen Sharing", "File Sharing (SMB)"} {
		if !strings.Contains(out, name) {
			t.Errorf("sharing_status output missing service %q:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "open_settings") {
		t.Errorf("sharing_status output should point at the open_settings hand-off:\n%s", out)
	}
	// Each line carries a definite verdict.
	if strings.Count(out, "ON")+strings.Count(out, "OFF") < 3 {
		t.Errorf("expected an ON/OFF verdict for each of the three services:\n%s", out)
	}
}
