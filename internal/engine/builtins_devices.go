// builtins_devices.go implements two read-only "what's available / what's set"
// lookups that pair with the guided System Settings hand-offs in mutate_system.go:
//
//   - list_airplay_devices — which AirPlay receivers (Apple TVs, AirPlay-capable
//     TVs and speakers, other Macs) are reachable on the local network right now.
//     Answers "what can I mirror/stream to?"; starting the mirroring itself has no
//     first-party CLI and is handed off via open_settings (pane 'displays').
//   - list_input_sources — which keyboard layouts and input methods are enabled,
//     and which one is currently active. Answers "what languages can I type in?";
//     adding a new one is handed off via open_settings (pane 'keyboard').
//
// Both are fixed-argv trusted-binary reads with all parsing done in-process:
// neither takes a model-controlled parameter, so neither has any injection
// surface (there is nothing to place in an argv but constants).
package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// airplayBrowseWindow bounds how long we let dns-sd browse for AirPlay
// receivers. `dns-sd -B` is a *continuous* browser — it never exits on its own,
// printing a new line each time a service appears or disappears — so, exactly
// like the bounded ping sweep in builtins_network.go, we cap it with a short
// context deadline. Three seconds is enough for already-advertised receivers on
// a quiet home network to answer (they respond within tens of milliseconds)
// while keeping the tool snappy.
const airplayBrowseWindow = 3 * time.Second

// runListAirplayDevices browses Bonjour for AirPlay receivers on the local
// network and reports the unique device names found.
//
// The subtlety here is that dns-sd never terminates by itself, so we run it
// under a derived context with airplayBrowseWindow as its deadline. When that
// deadline fires it kills dns-sd, and execCommand deliberately returns the
// output captured up to that point ALONGSIDE the deadline error (see
// executor.go) — so the "timed out" error is the EXPECTED, successful path here,
// and we parse res.Stdout from it. A genuine failure (dns-sd could not be
// launched at all) is distinguished by the derived context NOT having hit its
// deadline.
//
// Note: the first time the host application browses the local network, macOS may
// show a one-time "wants to find devices on your local network" (Local Network)
// privacy prompt; until it is allowed, the browse returns nothing.
func runListAirplayDevices(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("dns-sd")
	if err != nil {
		return "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, airplayBrowseWindow)
	defer cancel()
	// Fixed argv: -B browses, _airplay._tcp is the AirPlay service type. No
	// model-controlled value is involved, so there is nothing to injection-guard.
	res, err := runCommand(runCtx, bin, "-B", "_airplay._tcp")

	// If the CALLER cancelled (client dropped the request), propagate that rather
	// than reporting a device list.
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	// The only error we expect is our own browse-window deadline firing; anything
	// else (with no captured output to fall back on) is a real launch failure.
	if err != nil && runCtx.Err() == nil {
		return "", err
	}
	if res == nil {
		return "", fmt.Errorf("list_airplay_devices: dns-sd produced no output")
	}
	return renderAirplayDevices(parseAirplayDevices(res.Stdout)), nil
}

// parseAirplayDevices extracts the distinct receiver names from `dns-sd -B`
// output. Each browse row looks like:
//
//	15:50:32.792  Add    3  11 local.  _airplay._tcp.  55" The Frame
//
// i.e. timestamp, an Add/Rmv action, flags, interface index, domain, service
// type, then the instance (device) name — which may itself contain spaces
// ("Jerry's MacBook Air", `55" The Frame`). We honour Add/Rmv so a receiver that
// announced then withdrew within the window is not reported, and de-duplicate
// because the same receiver is advertised once per network interface.
func parseAirplayDevices(stdout string) []string {
	present := make(map[string]bool)
	var order []string // preserves first-seen order for a stable listing
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		// A data row has at least: timestamp, action, flags, if, domain, service,
		// name. The header ("Timestamp A/R Flags ...") and status lines
		// ("...STARTING...") fail the action check below and are skipped.
		if len(fields) < 7 {
			continue
		}
		action := fields[1]
		if action != "Add" && action != "Rmv" {
			continue
		}
		// The device name is everything from the 7th column onward; Fields collapsed
		// runs of spaces, so re-join with a single space.
		name := strings.Join(fields[6:], " ")
		if name == "" {
			continue
		}
		if action == "Add" {
			if _, seen := present[name]; !seen {
				order = append(order, name)
			}
			present[name] = true
		} else { // Rmv
			present[name] = false
		}
	}

	var out []string
	for _, name := range order {
		if present[name] {
			out = append(out, name)
		}
	}
	return out
}

// renderAirplayDevices turns the parsed receiver names into a human-readable
// answer, including the empty case (which is normal: no receivers may be powered
// on, or the Local Network prompt may not yet be allowed).
func renderAirplayDevices(names []string) string {
	if len(names) == 0 {
		return "No AirPlay receivers were found on the local network. " +
			"None may be powered on, or the host app may still need Local Network access " +
			"(System Settings > Privacy & Security > Local Network)."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d AirPlay receiver(s) found on the local network:\n", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "  %s\n", n)
	}
	b.WriteString("\n(To start mirroring or streaming to one, use open_settings with pane 'displays' — " +
		"the AirPlay control is the '+' / display dropdown there; there is no command line to begin mirroring.)")
	return b.String()
}

// runListInputSources reports the enabled keyboard layouts and input methods and
// marks the one that is currently active. It reads two HIToolbox preference
// keys via `defaults`: AppleEnabledInputSources (everything the user has turned
// on) and AppleSelectedInputSources (what is active right now). The selected
// read is best-effort — if it fails we still list the enabled sources, just
// without a "current" marker — because listing the sources is the primary answer.
func runListInputSources(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("defaults")
	if err != nil {
		return "", err
	}
	// Fixed argv (domain + key are constants); no model input reaches the binary.
	enabled, err := runCommand(ctx, bin, "read", "com.apple.HIToolbox", "AppleEnabledInputSources")
	if err != nil {
		return "", err
	}
	if err := commandFailed("defaults", enabled); err != nil {
		return "", err
	}

	// Best-effort: a machine could conceivably lack the selected key; don't let
	// that sink the whole answer.
	var selectedText string
	if sel, err := runCommand(ctx, bin, "read", "com.apple.HIToolbox", "AppleSelectedInputSources"); err == nil && sel.ExitCode == 0 {
		selectedText = sel.Stdout
	}
	return renderInputSources(enabled.Stdout, selectedText), nil
}

// renderInputSources is the pure formatting half of runListInputSources, split
// out so it can be unit-tested against sample plist text. It lists the
// user-facing typing sources — keyboard layouts (e.g. "U.S.") and input modes
// (e.g. Pinyin, ITABC) — and marks whichever is currently active. Helper input
// methods with no user-selectable layout (character palette, press-and-hold,
// emoji) are intentionally omitted as noise; they never answer "what can I type
// in?".
func renderInputSources(enabledPlist, selectedPlist string) string {
	enabled := parsePlistDicts(enabledPlist)
	selectedKeys := make(map[string]bool)
	for _, d := range parsePlistDicts(selectedPlist) {
		if k := inputSourceKey(d); k != "" {
			selectedKeys[k] = true
		}
	}

	type source struct {
		label   string
		detail  string
		current bool
	}
	var sources []source
	for _, d := range enabled {
		switch d["InputSourceKind"] {
		case "Keyboard Layout":
			sources = append(sources, source{
				label:   d["KeyboardLayout Name"],
				detail:  "keyboard layout",
				current: selectedKeys[inputSourceKey(d)],
			})
		case "Input Mode":
			// Input Mode values are reverse-DNS ids like
			// "com.apple.inputmethod.SCIM.ITABC"; the last dot-component is the
			// human-recognisable name (ITABC, Pinyin). Keep the full id as detail.
			mode := d["Input Mode"]
			sources = append(sources, source{
				label:   lastDotComponent(mode),
				detail:  "input method: " + mode,
				current: selectedKeys[inputSourceKey(d)],
			})
		}
	}

	if len(sources) == 0 {
		// Unparseable or unexpectedly empty output — hand back the raw enabled
		// plist so the caller still sees something rather than a bare "none".
		return "Could not identify individual input sources; raw enabled-sources data:\n" +
			strings.TrimSpace(enabledPlist)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d input source(s) enabled:\n", len(sources))
	for _, s := range sources {
		label := s.label
		if label == "" {
			label = "(unnamed)"
		}
		marker := ""
		if s.current {
			marker = "  ← current"
		}
		fmt.Fprintf(&b, "  %s (%s)%s\n", label, s.detail, marker)
	}
	b.WriteString("\n(To add or remove a typing language, use open_settings with pane 'keyboard' — " +
		"Text Input > Input Sources > Edit, then '+'. Switching the active source has no command line; use the menu-bar Input menu.)")
	return b.String()
}

// inputSourceKey derives a stable identity for one input-source dictionary so an
// enabled entry can be matched against the selected list. An input mode is
// identified by its mode id, a keyboard layout by its name, otherwise the bundle
// id — enough to tell whether a given enabled source is the active one.
func inputSourceKey(d map[string]string) string {
	if m := d["Input Mode"]; m != "" {
		return "mode:" + m
	}
	if n := d["KeyboardLayout Name"]; n != "" {
		return "layout:" + n
	}
	if b := d["Bundle ID"]; b != "" {
		return "bundle:" + b
	}
	return ""
}

// lastDotComponent returns the final dot-separated segment of s (e.g. "Pinyin"
// from "com.apple.inputmethod.TCIM.Pinyin"), or s unchanged if it has no dot.
func lastDotComponent(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}

// parsePlistDicts is a minimal reader for the "old-style" (NeXTSTEP) property
// list that `defaults read` prints for an array of dictionaries — the format the
// HIToolbox input-source keys come back in, e.g.:
//
//	(
//	    {
//	        InputSourceKind = "Keyboard Layout";
//	        "KeyboardLayout Name" = "U.S.";
//	    },
//	    ...
//	)
//
// The standard library has no plist parser and the project deliberately avoids
// new dependencies, but this dialect is regular enough to read line by line:
// each "{" opens a dictionary, each "}" closes it, and every "key = value;" line
// in between contributes one entry (surrounding quotes and the trailing ";" are
// stripped). This is a pragmatic reader for exactly this shape, not a general
// plist parser — nested containers beyond this flat array-of-dicts are not needed
// and not handled.
func parsePlistDicts(text string) []map[string]string {
	var dicts []map[string]string
	var cur map[string]string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "{":
			cur = make(map[string]string)
		case strings.HasPrefix(line, "}"):
			if cur != nil {
				dicts = append(dicts, cur)
				cur = nil
			}
		default:
			if cur == nil {
				continue
			}
			eq := strings.Index(line, " = ")
			if eq < 0 {
				continue
			}
			key := unquotePlistToken(strings.TrimSpace(line[:eq]))
			val := strings.TrimSpace(line[eq+len(" = "):])
			val = unquotePlistToken(strings.TrimSpace(strings.TrimSuffix(val, ";")))
			cur[key] = val
		}
	}
	return dicts
}

// unquotePlistToken strips one layer of surrounding double quotes from an
// old-style plist token; bare tokens (unquoted identifiers and numbers) pass
// through unchanged.
func unquotePlistToken(s string) string {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return s[1 : len(s)-1]
	}
	return s
}
