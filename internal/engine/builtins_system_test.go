// builtins_system_test.go tests the pure parsing helpers behind the system
// status builtins — Wi-Fi interface resolution, Bluetooth JSON parsing, and the
// Low Power Mode line parse — against synthetic command output, with no live
// networksetup / system_profiler / pmset calls.
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
