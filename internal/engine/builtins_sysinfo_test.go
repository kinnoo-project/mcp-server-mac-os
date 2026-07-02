// builtins_sysinfo_test.go tests the machine-information builtins' pure
// parsing/rendering halves against canned command output (no subprocess), plus
// renderBatteryHealth (the power_status extension in builtins_system.go). The
// one test that runs the real binaries end to end is gated behind
// MCP_SYSINFO_LIVE=1, mirroring the network/process live-test convention.
package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestRenderAboutThisMac covers the hardware-overview rendering: an Apple
// Silicon profile surfaces model/chip/memory/serial, and unparseable profiler
// JSON degrades to just the sw_vers block instead of failing.
func TestRenderAboutThisMac(t *testing.T) {
	swVers := "ProductName:\t\tmacOS\nProductVersion:\t\t15.5\nBuildVersion:\t\t24F74\n"
	hw := `{"SPHardwareDataType":[{"machine_name":"MacBook Pro","machine_model":"Mac15,6",
	 "chip_type":"Apple M3 Pro","number_processors":"proc 11:5:6","physical_memory":"18 GB",
	 "serial_number":"TESTSERIAL123"}]}`

	got := renderAboutThisMac(swVers, hw)
	for _, want := range []string{"ProductVersion:\t\t15.5", "MacBook Pro — Mac15,6", "Chip: Apple M3 Pro", "Memory: 18 GB", "Serial number: TESTSERIAL123"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderAboutThisMac missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "CPU:") {
		t.Errorf("no cpu_type in input, so no CPU line expected:\n%s", got)
	}

	if got := renderAboutThisMac(swVers, "not json"); got != strings.TrimSpace(swVers) {
		t.Errorf("unparseable profiler JSON should degrade to sw_vers only, got:\n%s", got)
	}
}

// TestRenderDiskUsage covers the df -H rendering: device-backed volumes are
// kept (including a mount point containing spaces), pseudo-filesystems are
// dropped, and unparseable output passes through verbatim.
func TestRenderDiskUsage(t *testing.T) {
	df := `Filesystem       Size   Used  Avail Capacity iused ifree %iused  Mounted on
/dev/disk3s1s1   994G    11G   537G     2%    426k  5.2G    0%   /
devfs            213k   213k     0B   100%     738     0  100%   /dev
/dev/disk3s5     994G   434G   537G    45%    4.5M  5.2G    0%   /System/Volumes/Data
/dev/disk5s1     2.0T   1.1T   947G    53%    1.2M  9.3G    0%   /Volumes/My Backup Disk
map auto_home      0B     0B     0B   100%       0     0     -   /System/Volumes/Data/home`

	got := renderDiskUsage(df)
	for _, want := range []string{
		"/ — 994G total, 11G used (2%), 537G free",
		"/System/Volumes/Data — 994G total, 434G used (45%), 537G free",
		"/Volumes/My Backup Disk — 2.0T total, 1.1T used (53%), 947G free",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderDiskUsage missing %q in:\n%s", want, got)
		}
	}
	for _, reject := range []string{"devfs", "auto_home"} {
		if strings.Contains(got, reject) {
			t.Errorf("pseudo-filesystem %q should be dropped:\n%s", reject, got)
		}
	}

	if got := renderDiskUsage("garbage"); got != "garbage" {
		t.Errorf("unparseable df output should pass through, got %q", got)
	}
}

// TestNthColumnOffset pins the column-walk helper the spacey-mount recovery
// depends on.
func TestNthColumnOffset(t *testing.T) {
	line := "a  bb\tccc  dd ee"
	cases := []struct {
		n    int
		want string
	}{
		{1, "a  bb\tccc  dd ee"}, {2, "bb\tccc  dd ee"}, {5, "ee"},
	}
	for _, tc := range cases {
		idx := nthColumnOffset(line, tc.n)
		if idx < 0 || line[idx:] != tc.want {
			t.Errorf("nthColumnOffset(%d) = %d (%q), want suffix %q", tc.n, idx, line[max(idx, 0):], tc.want)
		}
	}
	if idx := nthColumnOffset(line, 6); idx != -1 {
		t.Errorf("nthColumnOffset past the last column = %d, want -1", idx)
	}
}

// TestRenderSoftwareUpdateCheck covers the split-stream merge: the "no new
// software" verdict arrives on stderr behind a progress banner that must be
// dropped, while found updates arrive on stdout.
func TestRenderSoftwareUpdateCheck(t *testing.T) {
	noneStderr := "Software Update Tool\n\nFinding available software\nNo new software available.\n"
	if got := renderSoftwareUpdateCheck("", noneStderr); got != "No new software available." {
		t.Errorf("no-updates verdict = %q", got)
	}

	updatesStdout := "Software Update found the following new or updated software:\n* Label: macOS Sequoia 15.6-24G84\n\tTitle: macOS Sequoia 15.6\n"
	got := renderSoftwareUpdateCheck(updatesStdout, "Software Update Tool\nFinding available software\n")
	if !strings.Contains(got, "macOS Sequoia 15.6") || strings.Contains(got, "Software Update Tool") {
		t.Errorf("updates rendering wrong:\n%s", got)
	}

	if got := renderSoftwareUpdateCheck("", ""); !strings.Contains(got, "no output") {
		t.Errorf("empty streams should say so, got %q", got)
	}
}

// TestRenderBatteryHealth covers the power_status battery-health extension: a
// laptop profile yields one summary line, and a batteryless (desktop) or
// unparseable profile yields "" so the line is omitted.
func TestRenderBatteryHealth(t *testing.T) {
	laptop := `{"SPPowerDataType":[
	 {"sppower_battery_health_info":{"sppower_battery_cycle_count":123,
	  "sppower_battery_health":"Good","sppower_battery_health_maximum_capacity":"87%"}},
	 {"AC Charger Information":{}}]}`
	got := renderBatteryHealth(laptop)
	for _, want := range []string{"condition Good", "cycle count 123", "maximum capacity 87%"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderBatteryHealth missing %q in %q", want, got)
		}
	}

	if got := renderBatteryHealth(`{"SPPowerDataType":[{"AC Charger Information":{}}]}`); got != "" {
		t.Errorf("no battery should render nothing, got %q", got)
	}
	if got := renderBatteryHealth("not json"); got != "" {
		t.Errorf("unparseable JSON should render nothing, got %q", got)
	}
}

// TestSysinfoBuiltins_Live runs about_this_mac and disk_usage against the real
// machine. Gated: slowish subprocesses and machine-specific output.
// software_update_check is deliberately NOT run even here — it contacts
// Apple's servers and can take a minute.
func TestSysinfoBuiltins_Live(t *testing.T) {
	if os.Getenv("MCP_SYSINFO_LIVE") != "1" {
		t.Skip("set MCP_SYSINFO_LIVE=1 to run the live sysinfo builtins")
	}
	ctx := context.Background()
	about, err := runAboutThisMac(ctx, registry.Capability{}, nil)
	if err != nil || !strings.Contains(about, "ProductVersion") {
		t.Errorf("about_this_mac live: err=%v out=%q", err, about)
	}
	disk, err := runDiskUsage(ctx, registry.Capability{}, nil)
	if err != nil || !strings.Contains(disk, "free") {
		t.Errorf("disk_usage live: err=%v out=%q", err, disk)
	}
}
