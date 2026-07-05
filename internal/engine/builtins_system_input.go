// builtins_system_input.go implements two read-only `system` probes added in V9:
//
//   - key_remap_status — what keyboard remapping is in effect right now (hidutil)
//   - sharing_status    — are Remote Login (SSH), Screen Sharing, and File
//     Sharing turned on?
//
// # key_remap_status
//
// It reads macOS's current UserKeyMapping via `hidutil property --get` and
// renders it for a human: naming the remap when the mapping matches one of the
// curated remaps remap_key can apply (e.g. "Caps Lock → Escape"), and otherwise
// listing the raw source→destination key pairs. The mutating counterpart
// (remap_key) shares the parser and the curated table from mutate_system_input.go.
//
// # sharing_status — why a loopback port probe, not lsof/launchctl
//
// The obvious ways to check these services are unreliable WITHOUT root: a
// non-root `lsof -i` on macOS only sees the CURRENT user's own sockets, but
// sshd/screensharingd/smbd all run as root, so lsof would report them "off" even
// when on; and `launchctl print system/<label>` is likewise root-gated. What DOES
// work unprivileged is asking the kernel to connect: enabling any of these
// services makes its daemon listen on a well-known TCP port (22, 5900, 445) on
// ALL interfaces, including loopback. So sharing_status attempts a short,
// immediately-closed TCP connection to 127.0.0.1 on each port; a successful
// connect means the service is listening (on), a refusal means nothing is there
// (off). This is a pure in-process check — no subprocess, no input, no injection
// surface. Its one documented limitation: a service deliberately configured to
// bind only a non-loopback interface would read as off, but the standard macOS
// toggles always bind loopback too, so in practice the signal is exact.
package engine

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// hidMappingFieldPattern extracts one "HIDKeyboardModifierMappingSrc = 12345" or
// "…Dst = 12345" assignment from `hidutil property --get UserKeyMapping` output,
// which is an old-style plist array of dicts with DECIMAL values, e.g.
//
//	(
//	    {
//	        HIDKeyboardModifierMappingDst = 30064771113;
//	        HIDKeyboardModifierMappingSrc = 30064771129;
//	    }
//	)
//
// The capture groups are the field suffix ("Src"/"Dst") and the decimal value.
var hidMappingFieldPattern = regexp.MustCompile(`HIDKeyboardModifierMapping(Src|Dst)\s*=\s*(\d+)`)

// parseUserKeyMapping turns `hidutil property --get UserKeyMapping` output into
// the source→destination pairs it encodes. An empty mapping — reported as either
// "(null)" or an empty "()" array — yields no pairs. Fields are read in document
// order and assembled into pairs as each Src/Dst is completed, which matches
// hidutil's per-dict layout; a stray field with no partner is dropped rather than
// paired with an unrelated one. This is the state-capture step remap_key's inverse
// relies on, and the parse key_remap_status renders.
func parseUserKeyMapping(getOutput string) []keyPair {
	var pairs []keyPair
	var haveSrc, haveDst bool
	var src, dst uint64
	for _, m := range hidMappingFieldPattern.FindAllStringSubmatch(getOutput, -1) {
		v, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			continue // value outside uint64; skip rather than mispair
		}
		switch m[1] {
		case "Src":
			src, haveSrc = v, true
		case "Dst":
			dst, haveDst = v, true
		}
		if haveSrc && haveDst {
			pairs = append(pairs, keyPair{src: src, dst: dst})
			haveSrc, haveDst = false, false
		}
	}
	return pairs
}

// hidUsageNames maps the modifier-key HID usage codes the curated remaps touch to
// human labels, so key_remap_status can say "Caps Lock → Escape" instead of two
// 11-digit numbers. An unknown code falls back to its hex form (see hidUsageName).
var hidUsageNames = map[uint64]string{
	hidCapsLock:     "Caps Lock",
	hidEscape:       "Escape",
	hidLeftControl:  "Left Control",
	hidLeftAlt:      "Left Option",
	hidLeftGUI:      "Left Command",
	hidRightControl: "Right Control",
	hidRightAlt:     "Right Option",
	hidRightGUI:     "Right Command",
	hidNoEvent:      "(disabled — no key)",
}

// hidUsageName renders a HID usage code as a human label, falling back to its
// hex representation for a code outside the curated set (so a custom mapping the
// user set up themselves still displays intelligibly rather than as opaque
// decimal).
func hidUsageName(code uint64) string {
	if name, ok := hidUsageNames[code]; ok {
		return name
	}
	return fmt.Sprintf("0x%X", code)
}

// describeRemapPairs renders a set of remap pairs as a readable, comma-separated
// list of "From → To" arrows for previews and status output.
func describeRemapPairs(pairs []keyPair) string {
	if len(pairs) == 0 {
		return "no remapping (default keyboard)"
	}
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%s → %s", hidUsageName(p.src), hidUsageName(p.dst)))
	}
	return strings.Join(parts, ", ")
}

// describePriorMapping renders the mapping captured at stage time for a remap_key
// preview: it names the curated remap when the prior state matches one, so undo
// reads as "restore Caps Lock → Escape" rather than a raw pair dump.
func describePriorMapping(pairs []keyPair) string {
	if len(pairs) == 0 {
		return "no custom remapping"
	}
	if name := matchCuratedRemap(pairs); name != "" {
		return fmt.Sprintf("%s: %s", name, describeRemapPairs(pairs))
	}
	return describeRemapPairs(pairs)
}

// matchCuratedRemap returns the name of the curated remap whose pair set equals
// `pairs` (order-independent), or "" if none matches. It lets key_remap_status
// and previews label a known remap instead of only dumping its raw pairs.
func matchCuratedRemap(pairs []keyPair) string {
	for name, curated := range curatedRemaps {
		if sameKeyPairSet(pairs, curated) {
			return name
		}
	}
	return ""
}

// sameKeyPairSet reports whether two pair lists contain exactly the same
// source→destination pairs, regardless of order (hidutil does not preserve the
// order we set, so an order-sensitive compare would spuriously miss a match).
func sameKeyPairSet(a, b []keyPair) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[keyPair]int, len(a))
	for _, p := range a {
		counts[p]++
	}
	for _, p := range b {
		counts[p]--
		if counts[p] < 0 {
			return false
		}
	}
	return true
}

// probeUserKeyMapping runs a read-only `hidutil property --get UserKeyMapping`
// and returns its raw output. It is the state-capture step remap_key stages its
// inverse from, and the read key_remap_status renders — kept as a shared helper so
// both go through the same pinned, verb-confined invocation.
func probeUserKeyMapping(ctx context.Context) (string, error) {
	bin, err := policy.ResolveBinary("hidutil")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, hidutilGetArgs()...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("hidutil exited with status %d", res.ExitCode)
		}
		return "", fmt.Errorf("hidutil property --get UserKeyMapping: %s", detail)
	}
	return res.Stdout, nil
}

// runKeyRemapStatus reports the keyboard remapping currently in effect. It reads
// UserKeyMapping via hidutil, names the remap when it matches a curated one, and
// otherwise lists the raw source→destination pairs. A default (empty) mapping is
// reported plainly so the answer to "is Caps Lock remapped?" is unambiguous.
func runKeyRemapStatus(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	raw, err := probeUserKeyMapping(ctx)
	if err != nil {
		return "", err
	}
	pairs := parseUserKeyMapping(raw)
	if len(pairs) == 0 {
		return "No custom keyboard remapping is in effect — the keyboard is at its defaults.", nil
	}
	var b strings.Builder
	if name := matchCuratedRemap(pairs); name != "" {
		fmt.Fprintf(&b, "A keyboard remap is in effect (%s):\n", name)
	} else {
		b.WriteString("A custom keyboard remap is in effect:\n")
	}
	for _, p := range pairs {
		fmt.Fprintf(&b, "  %s → %s\n", hidUsageName(p.src), hidUsageName(p.dst))
	}
	b.WriteString("\n(Key remappings are cleared by a reboot.)")
	return b.String(), nil
}

// sharedService is one sharing feature and the TCP port its daemon listens on
// when enabled.
type sharedService struct {
	name string
	port int
}

// sharingServices is the fixed set sharing_status probes. The ports are the
// well-known ones each macOS sharing daemon binds when its toggle is on:
// Remote Login → sshd:22, Screen Sharing → screensharingd:5900, File Sharing →
// smbd:445.
var sharingServices = []sharedService{
	{"Remote Login (SSH)", 22},
	{"Screen Sharing", 5900},
	{"File Sharing (SMB)", 445},
}

// sharingProbeTimeout bounds each loopback connect attempt. A loopback connection
// is accepted or refused effectively instantly, so this is only a backstop
// against a pathological hang; it is layered under the request context, whichever
// fires first.
const sharingProbeTimeout = 500 * time.Millisecond

// runSharingStatus reports whether each main sharing service is on by probing its
// loopback port (see the file header for why this beats lsof/launchctl without
// root). It takes no input, so there is no injection surface.
func runSharingStatus(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	var b strings.Builder
	b.WriteString("Sharing services (detected by checking this Mac's own listening ports):\n")
	for _, s := range sharingServices {
		state := "OFF"
		if probeLoopbackPort(ctx, s.port) {
			state = "ON"
		}
		fmt.Fprintf(&b, "  %-22s %s (port %d)\n", s.name+":", state, s.port)
	}
	b.WriteString("\nTo turn any of these on or off (each needs administrator rights), " +
		"use open_settings (pane 'sharing').")
	return strings.TrimRight(b.String(), "\n"), nil
}

// probeLoopbackPort reports whether something is listening on 127.0.0.1:port by
// attempting a TCP connection and immediately closing it. A successful dial means
// a listener is present (the service is on); a refusal means none is (off). The
// dial is bound to both the request context and a short timeout so it can never
// hang. No data is sent — the connection is opened and closed at once.
func probeLoopbackPort(ctx context.Context, port int) bool {
	dialCtx, cancel := context.WithTimeout(ctx, sharingProbeTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
