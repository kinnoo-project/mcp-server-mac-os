// builtins_system_test.go tests the pure parsing helpers behind the system
// status builtins — Wi-Fi interface resolution, Wi-Fi status rendering (joined
// network + signal strength, with neighbor-SSID and AirDrop-interface exclusion),
// Bluetooth JSON parsing, and the Low Power Mode line parse — against synthetic
// command output, with no live networksetup / system_profiler / pmset calls.
package engine

import (
	"strings"
	"testing"
)

func TestParseWifiDevice(t *testing.T) {
	out := "Hardware Port: Ethernet\nDevice: en1\nEthernet Address: aa:bb\n\nHardware Port: Wi-Fi\nDevice: en0\nEthernet Address: cc:dd\n"
	if got := parseWifiDevice(out); got != "en0" {
		t.Errorf("parseWifiDevice = %q, want en0", got)
	}
	if got := parseWifiDevice("Hardware Port: Ethernet\nDevice: en1\n"); got != "" {
		t.Errorf("parseWifiDevice with no Wi-Fi = %q, want empty", got)
	}
}

// wifiProfilerSample mirrors the shape of `system_profiler SPAirPortDataType
// -json`: a Wi-Fi interface (en0) joined to a network with a signal reading, a
// peer-to-peer awdl0 (AirDrop) interface that also carries a current-network
// block but no SSID, and a neighbor network in range. The renderer must key off
// en0, must never surface awdl0 as "connected", and must never leak the
// neighbor SSID. The SSIDs here are fabricated placeholders, not real networks.
const wifiProfilerSample = `{"SPAirPortDataType":[{"spairport_airport_interfaces":[
  {"_name":"en0",
   "spairport_status_information":"spairport_status_connected",
   "spairport_current_network_information":{"_name":"PlaceholderNet","spairport_network_channel":"7 (2GHz, 20MHz)","spairport_signal_noise":"-42 dBm / -88 dBm"},
   "spairport_airport_other_local_wireless_networks":[{"_name":"NeighborSecretNet"}]},
  {"_name":"awdl0",
   "spairport_current_network_information":{"spairport_network_type":"spairport_network_type_station"}}
]}]}`

func TestRenderWifiStatus_Connected(t *testing.T) {
	out := renderWifiStatus("en0", "Wi-Fi Power (en0): On", []byte(wifiProfilerSample))
	if !strings.Contains(out, "Connected to: PlaceholderNet") {
		t.Errorf("expected joined SSID reported, got: %s", out)
	}
	// The signal must be reported as dBm with a plain-language quality rating so
	// the model can answer "is my signal good?" -42 dBm falls in the top bucket.
	if !strings.Contains(out, "-42 dBm") || !strings.Contains(out, "excellent") {
		t.Errorf("expected RSSI + quality rating, got: %s", out)
	}
	// A neighbor's SSID is private and irrelevant; it must never leak out.
	if strings.Contains(out, "NeighborSecretNet") {
		t.Errorf("neighbor SSID must not be reported, got: %s", out)
	}
	// awdl0 (AirDrop) must never be mistaken for the joined network.
	if strings.Contains(out, "awdl0") {
		t.Errorf("peer-to-peer interface must not appear, got: %s", out)
	}
}

func TestRenderWifiStatus_NotConnected(t *testing.T) {
	// Radio on, but en0 reports no current network (no SSID) — the genuine
	// not-joined case. It must NOT claim a network, and must NOT reproduce the
	// old false-negative by asserting anything a permission artifact could fake.
	notJoined := `{"SPAirPortDataType":[{"spairport_airport_interfaces":[{"_name":"en0","spairport_current_network_information":{"spairport_network_type":"spairport_network_type_station"}}]}]}`
	out := renderWifiStatus("en0", "Wi-Fi Power (en0): On", []byte(notJoined))
	if !strings.Contains(out, "Not currently joined") {
		t.Errorf("expected not-joined message, got: %s", out)
	}
}

func TestRenderWifiStatus_RadioOff(t *testing.T) {
	// When the radio is off there is nothing to be joined to; the renderer must
	// short-circuit and not append a confusing network line.
	out := renderWifiStatus("en0", "Wi-Fi Power (en0): Off", []byte(wifiProfilerSample))
	if strings.Contains(out, "Connected to") || strings.Contains(out, "Not currently joined") {
		t.Errorf("radio-off output should omit network status, got: %s", out)
	}
	if !strings.Contains(out, "Off") {
		t.Errorf("expected power Off reported, got: %s", out)
	}
}

func TestRenderWifiStatus_ProfilerUnavailable(t *testing.T) {
	// If the system_profiler probe failed (nil bytes) we must degrade to a
	// truthful "can't name a network" rather than crash or fabricate one.
	out := renderWifiStatus("en0", "Wi-Fi Power (en0): On", nil)
	if !strings.Contains(out, "Not currently joined") {
		t.Errorf("expected graceful degrade, got: %s", out)
	}
}

func TestParseRSSI(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"-42 dBm / -88 dBm", -42, true},
		{"-73 dBm / -90 dBm", -73, true},
		{"", 0, false},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := parseRSSI(c.in)
		if ok != c.wantOK || (ok && got != c.want) {
			t.Errorf("parseRSSI(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestDescribeSignal(t *testing.T) {
	cases := []struct {
		rssi int
		want string
	}{
		{-30, "excellent"},
		{-50, "excellent"},
		{-55, "good"},
		{-65, "fair"},
		{-75, "weak"},
		{-90, "very weak"},
	}
	for _, c := range cases {
		if got := describeSignal(c.rssi); got != c.want {
			t.Errorf("describeSignal(%d) = %q, want %q", c.rssi, got, c.want)
		}
	}
}

func TestRenderBluetoothStatus(t *testing.T) {
	on := `{"SPBluetoothDataType":[{"controller_properties":{"controller_state":"attrib_on"},"device_connected":[{"Magic Keyboard":{}},{"AirPods Pro":{}}],"device_not_connected":[{"Old Mouse":{}}]}]}`
	out, err := renderBluetoothStatus([]byte(on))
	if err != nil {
		t.Fatalf("renderBluetoothStatus: %v", err)
	}
	if !strings.Contains(out, "Bluetooth is ON.") {
		t.Errorf("expected ON state, got: %s", out)
	}
	for _, dev := range []string{"Magic Keyboard", "AirPods Pro"} {
		if !strings.Contains(out, dev) {
			t.Errorf("expected connected device %q in: %s", dev, out)
		}
	}
	// Paired-but-not-connected devices answer "what is paired to this Mac?".
	if !strings.Contains(out, "Old Mouse") || !strings.Contains(out, "paired device(s) not currently connected") {
		t.Errorf("expected paired-not-connected device listed, got: %s", out)
	}

	off := `{"SPBluetoothDataType":[{"controller_properties":{"controller_state":"attrib_off"}}]}`
	out, err = renderBluetoothStatus([]byte(off))
	if err != nil {
		t.Fatalf("renderBluetoothStatus(off): %v", err)
	}
	if !strings.Contains(out, "Bluetooth is OFF.") || !strings.Contains(out, "No devices are currently connected.") {
		t.Errorf("expected OFF + no devices, got: %s", out)
	}
}

func TestRenderPowerStatus(t *testing.T) {
	batt := "Now drawing from 'AC Power'\n -InternalBattery-0 (id=123)\t100%; charged; 0:00 remaining present: true"
	settings := "System-wide power settings:\nCurrently in use:\n lowpowermode         1\n hibernatemode        3\n"
	out := renderPowerStatus(batt, settings)
	if !strings.Contains(out, "AC Power") || !strings.Contains(out, "100%") {
		t.Errorf("expected battery summary passed through, got: %s", out)
	}
	if !strings.Contains(out, "Low Power Mode: on") {
		t.Errorf("expected Low Power Mode on, got: %s", out)
	}
	if out := renderPowerStatus(batt, " lowpowermode         0\n"); !strings.Contains(out, "Low Power Mode: off") {
		t.Errorf("expected Low Power Mode off, got: %s", out)
	}
}
