// builtins_system.go implements the read-only system-status builtins: Wi-Fi,
// Bluetooth, and power/battery state. These answer "what's the state of this
// machine's radios and power?" and pair with open_settings (mutate_system.go),
// which hands the user off to System Settings for the toggles that need
// administrator rights and so cannot be performed over this transport.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// runWifiStatus reports the Wi-Fi radio power and the joined network. It first
// resolves the Wi-Fi interface name (usually en0, but not guaranteed) from the
// hardware-port listing, then queries that interface — never assuming a fixed
// device name.
func runWifiStatus(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("networksetup")
	if err != nil {
		return "", err
	}
	ports, err := runCommand(ctx, bin, "-listallhardwareports")
	if err != nil {
		return "", err
	}
	dev := parseWifiDevice(ports.Stdout)
	if dev == "" {
		return "No Wi-Fi interface was found on this Mac.", nil
	}

	power, err := runCommand(ctx, bin, "-getairportpower", dev)
	if err != nil {
		return "", err
	}
	network, err := runCommand(ctx, bin, "-getairportnetwork", dev)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Wi-Fi interface: %s\n", dev)
	b.WriteString(strings.TrimSpace(power.Stdout))
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(network.Stdout))
	b.WriteString("\n")
	return b.String(), nil
}

// parseWifiDevice extracts the Wi-Fi interface name from `networksetup
// -listallhardwareports` output, which lists each port as a "Hardware Port:"
// line followed by a "Device:" line. It returns the Device value that follows
// the "Wi-Fi" port, or "" if there is none.
func parseWifiDevice(stdout string) string {
	lines := strings.Split(stdout, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "Hardware Port: Wi-Fi" {
			for _, next := range lines[i+1:] {
				if dev, ok := strings.CutPrefix(strings.TrimSpace(next), "Device: "); ok {
					return strings.TrimSpace(dev)
				}
			}
		}
	}
	return ""
}

// runListPreferredWifi lists the remembered Wi-Fi networks in priority order.
func runListPreferredWifi(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("networksetup")
	if err != nil {
		return "", err
	}
	ports, err := runCommand(ctx, bin, "-listallhardwareports")
	if err != nil {
		return "", err
	}
	dev := parseWifiDevice(ports.Stdout)
	if dev == "" {
		return "No Wi-Fi interface was found on this Mac.", nil
	}
	res, err := runCommand(ctx, bin, "-listpreferredwirelessnetworks", dev)
	if err != nil {
		return "", err
	}
	// networksetup prints a header line ("Preferred networks on en0:") followed
	// by one tab-indented SSID per line; pass it through trimmed.
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		return "No preferred Wi-Fi networks are configured.", nil
	}
	return out, nil
}

// runBluetoothStatus reports Bluetooth power and connected devices, parsed from
// system_profiler's JSON so no brittle text scraping is needed.
func runBluetoothStatus(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("system_profiler")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "SPBluetoothDataType", "-json")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("bluetooth_status: system_profiler exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return renderBluetoothStatus([]byte(res.Stdout))
}

// renderBluetoothStatus is the pure parsing half of runBluetoothStatus, split out
// so it can be unit-tested against a sample payload. It reads the controller
// power state and the names of connected devices, tolerating the field's absence
// (an older/odd profiler payload) rather than failing.
func renderBluetoothStatus(jsonBytes []byte) (string, error) {
	var report struct {
		SPBluetoothDataType []struct {
			ControllerProperties struct {
				State string `json:"controller_state"`
			} `json:"controller_properties"`
			DeviceConnected []map[string]json.RawMessage `json:"device_connected"`
		} `json:"SPBluetoothDataType"`
	}
	if err := json.Unmarshal(jsonBytes, &report); err != nil {
		return "", fmt.Errorf("bluetooth_status: could not parse system_profiler output: %w", err)
	}
	if len(report.SPBluetoothDataType) == 0 {
		return "No Bluetooth controller was found on this Mac.", nil
	}

	entry := report.SPBluetoothDataType[0]
	var b strings.Builder
	switch entry.ControllerProperties.State {
	case "attrib_on":
		b.WriteString("Bluetooth is ON.\n")
	case "attrib_off":
		b.WriteString("Bluetooth is OFF.\n")
	default:
		b.WriteString("Bluetooth power state is unknown.\n")
	}

	// Each connected-device entry is a single-key object: { "<Device Name>": {...} }.
	var names []string
	for _, dev := range entry.DeviceConnected {
		for name := range dev {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		b.WriteString("No devices are currently connected.")
	} else {
		fmt.Fprintf(&b, "%d connected device(s):\n", len(names))
		for _, n := range names {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	return b.String(), nil
}

// runPowerStatus reports power source, battery state, and Low Power Mode.
func runPowerStatus(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("pmset")
	if err != nil {
		return "", err
	}
	batt, err := runCommand(ctx, bin, "-g", "batt")
	if err != nil {
		return "", err
	}
	settings, err := runCommand(ctx, bin, "-g")
	if err != nil {
		return "", err
	}
	return renderPowerStatus(batt.Stdout, settings.Stdout), nil
}

// renderPowerStatus is the pure formatting half of runPowerStatus. It passes
// through pmset's already-readable battery summary and appends an explicit Low
// Power Mode line parsed from `pmset -g`, so the model gets a single clear answer.
func renderPowerStatus(battOut, settingsOut string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(battOut))
	b.WriteString("\n")

	lpm := "unknown"
	for _, line := range strings.Split(settingsOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "lowpowermode" {
			switch fields[1] {
			case "1":
				lpm = "on"
			case "0":
				lpm = "off"
			default:
				lpm = fields[1]
			}
		}
	}
	fmt.Fprintf(&b, "Low Power Mode: %s", lpm)
	return b.String()
}
