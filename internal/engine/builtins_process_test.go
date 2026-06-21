// builtins_process_test.go tests the pure parsing, classification, and
// formatting helpers behind the process/resource builtins against synthetic
// command output — no live ps/sysctl/vm_stat/ioreg/launchctl calls (those are
// exercised only by the env-gated TestProcessBuiltins_Live). The focus is the
// fragile bits: parsing space-bearing executable paths out of ps, scaling
// vm_stat page counts, pulling the GPU counters out of ioreg's one-line dict,
// and the system-vs-user origin heuristic.
package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func TestParsePsRows(t *testing.T) {
	out := `  PID  PPID USER              %CPU %MEM     ELAPSED STAT COMM
    1     0 root               0.1  0.2 12-00:00:00 Ss   /sbin/launchd
  392     1 _windowserver     27.5  0.6 12-00:33:24 Ss   /System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer
 4111     1 jerry              5.8  1.5    01:12.34 S    /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
 9999  4111 jerry              0.0  0.0       00:01 Z    (defunct)`
	rows := parsePsRows(out)
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	// The header must be skipped and the space-bearing app path preserved whole.
	chrome := rows[2]
	if chrome.pid != 4111 || chrome.ppid != 1 || chrome.user != "jerry" {
		t.Errorf("chrome row identity wrong: %+v", chrome)
	}
	if chrome.pcpu != 5.8 || chrome.pmem != 1.5 {
		t.Errorf("chrome row resources wrong: %+v", chrome)
	}
	if chrome.command != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
		t.Errorf("space-bearing command path not preserved: %q", chrome.command)
	}
	if rows[3].state[0] != 'Z' {
		t.Errorf("expected zombie state on last row, got %q", rows[3].state)
	}
}

func TestClassifyOrigin(t *testing.T) {
	cases := map[string]string{
		"/System/Library/CoreServices/Dock.app/Contents/MacOS/Dock": "system",
		"/sbin/launchd":         "system",
		"/usr/libexec/secinitd": "system",
		"/usr/bin/ssh":          "system",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome": "user",
		"/Users/jerry/bin/mytool":                                      "user",
		"/usr/local/bin/node":                                          "user",
		"/opt/homebrew/bin/rg":                                         "user",
		"":                                                             "other",
		"(defunct)":                                                    "other",
	}
	for path, want := range cases {
		if got := classifyOrigin(path); got != want {
			t.Errorf("classifyOrigin(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestAppNameFromExePath(t *testing.T) {
	cases := map[string]string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome": "Google Chrome",
		"/Applications/Safari.app/Contents/MacOS/Safari":               "Safari",
		// A nested helper resolves to its innermost bundle.
		"/Applications/Foo.app/Contents/Frameworks/Bar.app/Contents/MacOS/Bar": "Bar",
		"/usr/sbin/cupsd": "",
		"":                "",
	}
	for path, want := range cases {
		if got := appNameFromExePath(path); got != want {
			t.Errorf("appNameFromExePath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		512:         "512 B",
		1024:        "1.0 KB",
		17179869184: "16.0 GB",
	}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestParseVMStat(t *testing.T) {
	out := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                9168.
Pages active:                            327639.
Pages inactive:                          320956.
Pages speculative:                         1498.
Pages wired down:                        100000.
Pages occupied by compressor:             50000.`
	m := parseVMStat(out)
	// 9168 pages * 16384 bytes/page.
	if m["free"] != 9168*16384 {
		t.Errorf("free = %d, want %d", m["free"], 9168*16384)
	}
	if m["wired down"] != 100000*16384 {
		t.Errorf("wired down = %d, want %d", m["wired down"], 100000*16384)
	}
	if m["occupied by compressor"] != 50000*16384 {
		t.Errorf("compressor = %d, want %d", m["occupied by compressor"], 50000*16384)
	}
}

func TestParseLoadAvg(t *testing.T) {
	l1, l5, l15, ncpu := parseLoadAvg("{ 2.88 2.10 1.83 }\n10\n")
	if l1 != 2.88 || l5 != 2.10 || l15 != 1.83 || ncpu != 10 {
		t.Errorf("parseLoadAvg = %v %v %v ncpu=%d", l1, l5, l15, ncpu)
	}
}

func TestParseGPUStats(t *testing.T) {
	out := `    "PerformanceStatistics" = {"In use system memory (driver)"=0,"Tiler Utilization %"=28,"Renderer Utilization %"=30,"Device Utilization %"=71,"In use system memory"=407715840}`
	stats := parseGPUStats(out)
	if stats["Device Utilization %"] != 71 {
		t.Errorf("device util = %d, want 71", stats["Device Utilization %"])
	}
	if stats["Renderer Utilization %"] != 30 {
		t.Errorf("renderer util = %d, want 30", stats["Renderer Utilization %"])
	}
	if stats["In use system memory"] != 407715840 {
		t.Errorf("gpu mem = %d, want 407715840", stats["In use system memory"])
	}
	// "In use system memory (driver)" must not be confused with the plain key.
	if stats["In use system memory"] == 0 {
		t.Errorf("gpu mem incorrectly matched the (driver) variant")
	}
}

func TestParseLaunchctlList(t *testing.T) {
	out := `PID	Status	Label
-	0	com.apple.SafariHistoryServiceAgent
42907	-9	com.apple.progressd
1234	0	com.googlecode.iterm2
-	0	homebrew.mxcl.postgresql`
	all := parseLaunchctlList(out, false)
	if len(all) != 4 {
		t.Fatalf("all: expected 4 items, got %d", len(all))
	}
	thirdParty := parseLaunchctlList(out, true)
	if len(thirdParty) != 2 {
		t.Fatalf("third-party: expected 2 items, got %d (%v)", len(thirdParty), thirdParty)
	}
	for _, it := range thirdParty {
		if strings.HasPrefix(it.label, "com.apple.") {
			t.Errorf("third-party list leaked an Apple item: %q", it.label)
		}
	}
}

func TestLaunchdLabelForPID(t *testing.T) {
	out := `PID	Status	Label
-	0	com.apple.idle
42907	-9	com.apple.progressd
1234	0	com.googlecode.iterm2`
	if label, ok := launchdLabelForPID(out, 1234); !ok || label != "com.googlecode.iterm2" {
		t.Errorf("launchdLabelForPID(1234) = %q,%v", label, ok)
	}
	if _, ok := launchdLabelForPID(out, 99999); ok {
		t.Errorf("launchdLabelForPID(99999) should not match")
	}
}

func TestValidateProcessFilter(t *testing.T) {
	if err := validateProcessFilter("chrome"); err != nil {
		t.Errorf("plain filter rejected: %v", err)
	}
	if err := validateProcessFilter("a\x00b"); err == nil {
		t.Errorf("control-character filter should be rejected")
	}
}

func TestRunProcessInfo_RejectsLowPID(t *testing.T) {
	for _, pid := range []int{0, 1, -5} {
		_, err := runProcessInfo(context.Background(), registry.Capability{}, map[string]any{"pid": pid})
		if err == nil {
			t.Errorf("process_info with pid %d should be rejected", pid)
		}
	}
}

// TestProcessBuiltins_Live exercises the read-only builtins against the real
// machine. Skipped unless MCP_PROCESS_LIVE=1, mirroring the network domain's
// live-gated test, since the output depends on the host's actual state.
func TestProcessBuiltins_Live(t *testing.T) {
	if os.Getenv("MCP_PROCESS_LIVE") != "1" {
		t.Skip("set MCP_PROCESS_LIVE=1 to run the live process builtins")
	}
	ctx := context.Background()
	cap := registry.Capability{}

	if out, err := runListProcesses(ctx, cap, map[string]any{"sort_by": "memory", "limit": 5}); err != nil {
		t.Errorf("runListProcesses: %v", err)
	} else {
		t.Logf("list_processes:\n%s", out)
	}
	if out, err := runCpuLoad(ctx, cap, nil); err != nil {
		t.Errorf("runCpuLoad: %v", err)
	} else {
		t.Logf("cpu_load:\n%s", out)
	}
	if out, err := runMemoryStats(ctx, cap, nil); err != nil {
		t.Errorf("runMemoryStats: %v", err)
	} else {
		t.Logf("memory_stats:\n%s", out)
	}
	if out, err := runGPUStats(ctx, cap, nil); err != nil {
		t.Errorf("runGPUStats: %v", err)
	} else {
		t.Logf("gpu_stats:\n%s", out)
	}
	if out, err := runStartupItems(ctx, cap, nil); err != nil {
		t.Errorf("runStartupItems: %v", err)
	} else {
		t.Logf("startup_items:\n%s", out)
	}
	if out, err := runProcessInfo(ctx, cap, map[string]any{"pid": os.Getpid()}); err != nil {
		t.Errorf("runProcessInfo: %v", err)
	} else {
		t.Logf("process_info(self):\n%s", out)
	}
}
