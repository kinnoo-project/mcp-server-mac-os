// mutate_system_power.go implements the `system` domain's two hardware-power
// controls: display_sleep (blank the screen now) and wifi_set_power (turn the
// Wi-Fi radio on or off). Both change physical device state, so they live on the
// mutation seam alongside open_settings/notify/speak (mutate_system.go) rather
// than with the read-only status probes (builtins_system.go).
//
// # Why these two sit in different lanes
//
// display_sleep is a one-way, benign convenience: putting the display to sleep
// is instant and the ONLY way back is a human act (press a key, move the mouse),
// so there is no command to "undo" it. That makes it irreversible and — like
// open_settings — safe to run in the AUTO-COMMIT lane (no execute-token round
// trip): the worst case is a screen that the user immediately wakes.
//
// wifi_set_power is the opposite: turning Wi-Fi OFF severs the machine's network
// connectivity, which is exactly the kind of consequential, easy-to-regret change
// the stage→execute confirmation gate exists for. So it is STAGED (never
// auto-commit) and reversible — staging probes the radio's current power and
// bakes the restore-to-prior command into the inverse.
//
// # Admin rights
//
// Neither command needs administrator privileges on macOS 13+: `pmset
// displaysleepnow` and `networksetup -setairportpower` both run as the logged-in
// user. (This was the open contingency for wifi_set_power in the build plan; a
// live non-admin check confirmed `-setairportpower` does not prompt for a
// password — see docs/issues/issue-system-control-admin-gated-gaps.md.)
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// stageDisplaySleep stages (for immediate auto-commit) putting the display(s) to
// sleep via `pmset displaysleepnow`. There is no inverse — waking the display is
// a physical act the user performs — so the plan carries a nil Inverse and the
// preview says the action cannot be undone from software.
//
// The command takes no operands and no model input reaches argv (the whole
// invocation is a fixed constant), so there is no injection surface to guard.
func stageDisplaySleep(_ context.Context, _ registry.Capability, _ map[string]any) (*StagedPlan, error) {
	return &StagedPlan{
		// As with notify/speak, state only the reason here; the auto-commit path
		// appends its own "This cannot be undone." suffix for a nil-inverse op, so
		// repeating the phrase would duplicate the message.
		Preview: "Put the display to sleep immediately (the screen turns off). Waking it is a manual action — press a key or move the mouse.",
		Forward: Command{Binary: "pmset", Args: []string{"displaysleepnow"}},
		Inverse: nil, // irreversible from software: only a human can wake the display
	}, nil
}

// stageWifiSetPower stages turning the Wi-Fi radio on or off. It resolves the
// Wi-Fi interface name (never assuming "en0"), probes the radio's CURRENT power
// so the inverse can restore exactly that prior state, and returns a plan whose
// forward sets the requested state and whose inverse sets the observed one.
//
// This is staged (not auto-commit) because turning Wi-Fi off severs network
// connectivity — a consequential change the human-approval gate is meant to
// front. Injection: the `power` value is a closed on/off enum (validated by the
// registry and again here), and the device name comes from macOS's own
// hardware-port listing, not model input — so neither argv operand is
// attacker-controlled free text and there is no dash-leading value to guard.
func stageWifiSetPower(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	power, _ := getString(in, "power")
	var want string
	switch power {
	case "on":
		want = "on"
	case "off":
		want = "off"
	default:
		// Unreachable when staged through the registry (the enum is validated),
		// but defended because this also runs in direct unit tests.
		return nil, fmt.Errorf("wifi_set_power: unknown power state %q (want \"on\" or \"off\")", power)
	}

	bin, err := policy.ResolveBinary("networksetup")
	if err != nil {
		return nil, err
	}
	ports, err := runCommand(ctx, bin, "-listallhardwareports")
	if err != nil {
		return nil, err
	}
	// A non-zero exit surfaces in ExitCode, not err. Surface it before parsing:
	// otherwise a genuine failure (empty stdout) would be misread as "no Wi-Fi
	// interface found", masking the real cause — the same ExitCode discipline
	// probeWifiPower already applies below.
	if ports.ExitCode != 0 {
		return nil, fmt.Errorf("wifi_set_power: could not list hardware ports: exit %d: %s", ports.ExitCode, strings.TrimSpace(ports.Stderr))
	}
	dev := parseWifiDevice(ports.Stdout)
	if dev == "" {
		return nil, fmt.Errorf("wifi_set_power: no Wi-Fi interface was found on this Mac")
	}

	priorOn, err := probeWifiPower(ctx, bin, dev)
	if err != nil {
		return nil, err
	}
	prior := wifiPowerWord(priorOn)

	return planWifiSetPower(dev, want, prior), nil
}

// planWifiSetPower is the pure half of stageWifiSetPower: given the resolved
// device, the desired state, and the prior state observed at stage time, it
// assembles the forward/inverse commands and preview with no side effects. Split
// out so the argv layout and preview text can be unit-tested without a live
// networksetup probe.
func planWifiSetPower(dev, want, prior string) *StagedPlan {
	inverse := Command{Binary: "networksetup", Args: []string{"-setairportpower", dev, prior}}
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Turn Wi-Fi %s on interface %s (currently %s). Turning it off will disconnect this Mac from any Wi-Fi network. Undo will set it back to %s.",
			want, dev, prior, prior,
		),
		Forward: Command{Binary: "networksetup", Args: []string{"-setairportpower", dev, want}},
		Inverse: &inverse,
	}
}

// probeWifiPower runs a read-only `networksetup -getairportpower <dev>` and
// reports whether the radio is currently on. This is the state-capture step the
// inverse depends on: undo cannot know what to restore without first asking what
// the power state was. A value it cannot classify as on/off is an error, not a
// guess — staging must never bake an inverse it isn't sure of.
func probeWifiPower(ctx context.Context, bin, dev string) (bool, error) {
	res, err := runCommand(ctx, bin, "-getairportpower", dev)
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 {
		return false, fmt.Errorf("wifi_set_power: could not read the current Wi-Fi power state: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	on, ok := parseWifiPower(res.Stdout)
	if !ok {
		return false, fmt.Errorf("wifi_set_power: could not parse the current Wi-Fi power state from %q; refusing to stage an undo that might be wrong", strings.TrimSpace(res.Stdout))
	}
	return on, nil
}

// parseWifiPower extracts the on/off state from `networksetup -getairportpower
// <dev>` output, whose single line reads "Wi-Fi Power (en0): On". It returns the
// boolean and whether the line was recognised; an unrecognised shape returns
// ok=false so the caller can refuse rather than misclassify.
func parseWifiPower(stdout string) (on bool, ok bool) {
	line := strings.TrimSpace(stdout)
	switch {
	case strings.HasSuffix(line, ": On"):
		return true, true
	case strings.HasSuffix(line, ": Off"):
		return false, true
	default:
		return false, false
	}
}

// wifiPowerWord renders a power boolean as the literal token `networksetup
// -setairportpower` expects ("on"/"off").
func wifiPowerWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
