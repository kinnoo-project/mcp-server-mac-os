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
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// runWifiStatus reports the Wi-Fi radio power, the joined network, and the
// current signal strength.
//
// It deliberately draws the joined-network name and signal from
// `system_profiler SPAirPortDataType` rather than from
// `networksetup -getairportnetwork`. Since macOS Big Sur, `networksetup` (and
// the deprecated `airport` tool) return the literal string "You are not
// associated with an AirPort network." whenever the calling process has not
// been granted Location Services authorization — even when the Mac is in fact
// connected. That produced a false "not connected" answer with no hint that the
// result was a permission artifact rather than the truth. system_profiler's
// data type is not gated the same way and additionally exposes the RSSI/noise
// figures needed to answer "is my signal any good?".
//
// The Wi-Fi interface name and radio power are still read from `networksetup`,
// which report correctly without Location Services and give an authoritative
// on/off answer for the radio itself.
//
// No model-controlled input reaches argv here (this builtin takes no params;
// every argument is either a constant literal or the interface name parsed from
// system output), so there is no dash-leading/option-injection surface to guard.
func runWifiStatus(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	netsetup, err := policy.ResolveBinary("networksetup")
	if err != nil {
		return "", err
	}
	ports, err := runCommand(ctx, netsetup, "-listallhardwareports")
	if err != nil {
		return "", err
	}
	dev := parseWifiDevice(ports.Stdout)
	if dev == "" {
		return "No Wi-Fi interface was found on this Mac.", nil
	}

	power, err := runCommand(ctx, netsetup, "-getairportpower", dev)
	if err != nil {
		return "", err
	}

	// The joined-network name and signal come from system_profiler, whose
	// SSID visibility is not gated behind Location Services the way
	// networksetup's is. A profiler failure is non-fatal: we still report the
	// authoritative radio power we already have.
	var profilerJSON []byte
	if profiler, perr := policy.ResolveBinary("system_profiler"); perr == nil {
		if res, rerr := runCommand(ctx, profiler, "SPAirPortDataType", "-json"); rerr == nil && res.ExitCode == 0 {
			profilerJSON = []byte(res.Stdout)
		}
	}

	return renderWifiStatus(dev, power.Stdout, profilerJSON), nil
}

// renderWifiStatus is the pure formatting half of runWifiStatus, split out so it
// can be unit-tested against sample payloads. It takes the Wi-Fi interface name,
// the raw `networksetup -getairportpower` output (authoritative radio on/off),
// and the raw `system_profiler SPAirPortDataType -json` bytes (may be nil if the
// probe failed), and produces a plain-language status.
//
// It reports ONLY the currently-joined network — system_profiler also lists
// every neighboring SSID in range, which is private and irrelevant here, so
// those are never read or emitted.
func renderWifiStatus(dev, powerStdout string, profilerJSON []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Wi-Fi interface: %s\n", dev)
	b.WriteString(strings.TrimSpace(powerStdout))
	b.WriteString("\n")

	// If the radio is powered off there is nothing to be joined to; skip the
	// network lookup so we don't emit a confusing "not connected" alongside it.
	if strings.Contains(powerStdout, ": Off") {
		return b.String()
	}

	net, ok := parseCurrentWifiNetwork(profilerJSON, dev)
	if !ok {
		// Radio is on but system_profiler reported no current network. This is
		// the genuine "not connected" case OR (rarely) an SSID redaction; either
		// way we cannot name a network, so say so plainly without asserting a
		// false negative about connectivity.
		b.WriteString("Not currently joined to a Wi-Fi network.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Connected to: %s\n", net.SSID)
	if net.SignalRaw != "" {
		if rssi, parsed := parseRSSI(net.SignalRaw); parsed {
			fmt.Fprintf(&b, "Signal strength: %d dBm (%s)\n", rssi, describeSignal(rssi))
		} else {
			// Unexpected format — surface the raw figure rather than dropping it.
			fmt.Fprintf(&b, "Signal: %s\n", net.SignalRaw)
		}
	}
	if net.Channel != "" {
		fmt.Fprintf(&b, "Channel: %s\n", net.Channel)
	}
	return b.String()
}

// wifiNetwork is the subset of the current-network fields renderWifiStatus needs.
type wifiNetwork struct {
	SSID      string // the joined network's name
	SignalRaw string // e.g. "-42 dBm / -88 dBm" (RSSI / noise), verbatim from profiler
	Channel   string // e.g. "7 (2GHz, 20MHz)"
}

// parseCurrentWifiNetwork pulls the current-network block for interface dev out
// of system_profiler's SPAirPortDataType JSON. It returns ok=false when the
// bytes are empty/unparseable, when the interface isn't present, or when the
// interface reports no joined network (no SSID). Matching on the interface name
// is what excludes peer-to-peer interfaces like awdl0 (AirDrop), which also
// carry a current-network block but never a real SSID.
func parseCurrentWifiNetwork(jsonBytes []byte, dev string) (wifiNetwork, bool) {
	if len(jsonBytes) == 0 {
		return wifiNetwork{}, false
	}
	var report struct {
		SPAirPortDataType []struct {
			Interfaces []struct {
				Name           string `json:"_name"`
				CurrentNetwork struct {
					Name        string `json:"_name"`
					SignalNoise string `json:"spairport_signal_noise"`
					Channel     string `json:"spairport_network_channel"`
				} `json:"spairport_current_network_information"`
			} `json:"spairport_airport_interfaces"`
		} `json:"SPAirPortDataType"`
	}
	if err := json.Unmarshal(jsonBytes, &report); err != nil {
		return wifiNetwork{}, false
	}
	for _, block := range report.SPAirPortDataType {
		for _, iface := range block.Interfaces {
			if iface.Name != dev {
				continue
			}
			// A joined network always carries an SSID (_name); its absence means
			// the interface is on but not associated.
			if iface.CurrentNetwork.Name == "" {
				return wifiNetwork{}, false
			}
			return wifiNetwork{
				SSID:      iface.CurrentNetwork.Name,
				SignalRaw: iface.CurrentNetwork.SignalNoise,
				Channel:   iface.CurrentNetwork.Channel,
			}, true
		}
	}
	return wifiNetwork{}, false
}

// parseRSSI extracts the received-signal-strength value (in dBm) from
// system_profiler's "spairport_signal_noise" string, which is formatted as
// "<rssi> dBm / <noise> dBm" (e.g. "-42 dBm / -88 dBm"). It returns the RSSI as
// a negative integer and ok=true, or ok=false if the leading number can't be read.
func parseRSSI(signalNoise string) (int, bool) {
	// The RSSI is the first whitespace-separated token before "/".
	fields := strings.Fields(signalNoise)
	if len(fields) == 0 {
		return 0, false
	}
	rssi, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, false
	}
	return rssi, true
}

// describeSignal maps an RSSI (dBm) to a plain-language quality label so the
// model can answer "is my signal good?" directly. The thresholds follow the
// commonly-cited Wi-Fi signal scale: closer to 0 is stronger.
func describeSignal(rssi int) string {
	switch {
	case rssi >= -50:
		return "excellent"
	case rssi >= -60:
		return "good"
	case rssi >= -70:
		return "fair"
	case rssi >= -80:
		return "weak"
	default:
		return "very weak"
	}
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
// power state, the names of currently-connected devices, and the names of
// devices that are paired but not connected, tolerating any field's absence (an
// older/odd profiler payload) rather than failing.
func renderBluetoothStatus(jsonBytes []byte) (string, error) {
	var report struct {
		SPBluetoothDataType []struct {
			ControllerProperties struct {
				State string `json:"controller_state"`
			} `json:"controller_properties"`
			DeviceConnected    []map[string]json.RawMessage `json:"device_connected"`
			DeviceNotConnected []map[string]json.RawMessage `json:"device_not_connected"`
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

	connected := bluetoothDeviceNames(entry.DeviceConnected)
	if len(connected) == 0 {
		b.WriteString("No devices are currently connected.\n")
	} else {
		fmt.Fprintf(&b, "%d connected device(s):\n", len(connected))
		for _, n := range connected {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}

	// Paired-but-not-connected devices answer "what is paired to this Mac?".
	paired := bluetoothDeviceNames(entry.DeviceNotConnected)
	if len(paired) > 0 {
		fmt.Fprintf(&b, "%d paired device(s) not currently connected:\n", len(paired))
		for _, n := range paired {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}

	// Bluetooth cannot be toggled over this transport (no first-party CLI); the
	// model should hand the user off to System Settings for that.
	b.WriteString("(To turn Bluetooth on or off, use open_settings with pane 'bluetooth'.)")
	return b.String(), nil
}

// bluetoothDeviceNames flattens system_profiler's device list, where each entry
// is a single-key object: { "<Device Name>": {…} }.
func bluetoothDeviceNames(devices []map[string]json.RawMessage) []string {
	var names []string
	for _, dev := range devices {
		for name := range dev {
			names = append(names, name)
		}
	}
	return names
}

// runPowerStatus reports power source, battery state, Low Power Mode, and —
// on machines that have a battery — its health (condition, cycle count, and
// maximum-capacity percentage from system_profiler's power data).
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
	status := renderPowerStatus(batt.Stdout, settings.Stdout)

	// Battery health is best-effort: a desktop Mac has no battery, and a
	// profiler hiccup should not fail the whole power answer.
	if profiler, err := policy.ResolveBinary("system_profiler"); err == nil {
		if power, err := runCommand(ctx, profiler, "SPPowerDataType", "-json"); err == nil {
			if health := renderBatteryHealth(power.Stdout); health != "" {
				status += "\n" + health
			}
		}
	}
	return status, nil
}

// renderBatteryHealth extracts the battery-health summary from system_profiler
// SPPowerDataType -json output: condition (e.g. "Good"), cycle count, and the
// maximum-capacity percentage. It returns "" when the machine reports no
// battery or the JSON doesn't parse, letting the caller omit the line entirely.
func renderBatteryHealth(powerJSON string) string {
	var doc struct {
		Items []struct {
			HealthInfo *struct {
				CycleCount  int    `json:"sppower_battery_cycle_count"`
				Condition   string `json:"sppower_battery_health"`
				MaxCapacity string `json:"sppower_battery_health_maximum_capacity"`
			} `json:"sppower_battery_health_info"`
		} `json:"SPPowerDataType"`
	}
	if err := json.Unmarshal([]byte(powerJSON), &doc); err != nil {
		return ""
	}
	for _, item := range doc.Items {
		if item.HealthInfo == nil {
			continue
		}
		h := item.HealthInfo
		parts := []string{}
		if h.Condition != "" {
			parts = append(parts, "condition "+h.Condition)
		}
		if h.CycleCount > 0 {
			parts = append(parts, fmt.Sprintf("cycle count %d", h.CycleCount))
		}
		if h.MaxCapacity != "" {
			parts = append(parts, "maximum capacity "+h.MaxCapacity)
		}
		if len(parts) == 0 {
			return ""
		}
		return "Battery health: " + strings.Join(parts, ", ")
	}
	return ""
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
