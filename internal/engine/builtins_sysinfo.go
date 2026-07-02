// builtins_sysinfo.go implements the read-only machine-information builtins:
// what this Mac is (model, chip, memory, macOS version), how its disks are
// doing, and whether macOS updates are pending. They complement the radio/power
// status reads in builtins_system.go and, like them, run fixed-argv trusted
// binaries with all parsing done in-process — no model-controlled value ever
// reaches an argv, so these have no injection surface at all.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// commandFailed converts a non-zero exit into a real error carrying the tool
// name and its stderr. runCommand deliberately reports exit status as data
// (grep's "no match" is a legitimate result), but the builtins in this file
// parse one specific tool's output — rendering a FAILED run's partial output
// would silently mislead, so they must surface the failure instead.
func commandFailed(tool string, res *runResult) error {
	if res.ExitCode == 0 {
		return nil
	}
	msg := strings.TrimSpace(res.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(res.Stdout)
	}
	if msg == "" {
		msg = "no error output"
	}
	return fmt.Errorf("%s failed (exit %d): %s", tool, res.ExitCode, msg)
}

// runAboutThisMac answers "what Mac do I have?": the macOS version (sw_vers)
// plus the hardware overview (model, chip/CPU, memory, serial number) from
// system_profiler's hardware data type.
func runAboutThisMac(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	swVers, err := policy.ResolveBinary("sw_vers")
	if err != nil {
		return "", err
	}
	versOut, err := runCommand(ctx, swVers)
	if err != nil {
		return "", err
	}
	if err := commandFailed("sw_vers", versOut); err != nil {
		return "", err
	}
	profiler, err := policy.ResolveBinary("system_profiler")
	if err != nil {
		return "", err
	}
	hwOut, err := runCommand(ctx, profiler, "SPHardwareDataType", "-json")
	if err != nil {
		return "", err
	}
	if err := commandFailed("system_profiler", hwOut); err != nil {
		return "", err
	}
	return renderAboutThisMac(versOut.Stdout, hwOut.Stdout), nil
}

// renderAboutThisMac is the pure formatting half of runAboutThisMac: it passes
// the sw_vers block through and appends the interesting hardware fields parsed
// from system_profiler's JSON. Unparseable profiler output degrades to just the
// sw_vers block rather than failing — version info alone is still an answer.
func renderAboutThisMac(swVersOut, hwJSON string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(swVersOut))

	var doc struct {
		Items []struct {
			MachineName    string `json:"machine_name"`
			MachineModel   string `json:"machine_model"`
			ModelNumber    string `json:"model_number"`
			ChipType       string `json:"chip_type"`
			CPUType        string `json:"cpu_type"`
			NumberOfCPUs   string `json:"number_processors"`
			PhysicalMemory string `json:"physical_memory"`
			SerialNumber   string `json:"serial_number"`
		} `json:"SPHardwareDataType"`
	}
	if err := json.Unmarshal([]byte(hwJSON), &doc); err != nil || len(doc.Items) == 0 {
		return b.String()
	}
	hw := doc.Items[0]
	writeLine := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&b, "\n%s: %s", label, value)
		}
	}
	writeLine("Model", strings.TrimSpace(strings.Join(nonEmpty(hw.MachineName, hw.MachineModel), " — ")))
	// Apple Silicon reports chip_type; Intel Macs report cpu_type instead.
	writeLine("Chip", hw.ChipType)
	writeLine("CPU", hw.CPUType)
	writeLine("Cores", hw.NumberOfCPUs)
	writeLine("Memory", hw.PhysicalMemory)
	writeLine("Serial number", hw.SerialNumber)
	return b.String()
}

// nonEmpty filters out empty strings, letting renderAboutThisMac join whichever
// of the model fields are actually present.
func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// runDiskUsage reports free/used space for every real mounted volume via
// `df -H` (SI units, matching what Finder shows). One operation answers both
// "how much free space do I have?" and "what volumes are mounted?".
func runDiskUsage(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("df")
	if err != nil {
		return "", err
	}
	out, err := runCommand(ctx, bin, "-H")
	if err != nil {
		return "", err
	}
	if err := commandFailed("df", out); err != nil {
		return "", err
	}
	return renderDiskUsage(out.Stdout), nil
}

// renderDiskUsage is the pure formatting half of runDiskUsage. It keeps only
// device-backed rows (source under /dev/) — dropping pseudo-filesystems like
// devfs and the auto_home map that answer no user question — and re-renders
// each as one plain-language line. Unparseable output passes through verbatim
// so the model still sees whatever df reported.
func renderDiskUsage(dfOut string) string {
	lines := strings.Split(strings.TrimSpace(dfOut), "\n")
	if len(lines) < 2 {
		return strings.TrimSpace(dfOut)
	}
	var b strings.Builder
	b.WriteString("Mounted volumes (size, used, free):")
	kept := 0
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		// df -H rows: Filesystem Size Used Avail Capacity iused ifree %iused
		// Mounted-on. Nine columns minimum; only device-backed rows are kept.
		if len(fields) < 9 || !strings.HasPrefix(fields[0], "/dev/") {
			continue
		}
		// The mount point is the ninth column THROUGH END OF LINE — it may itself
		// contain spaces (e.g. "/Volumes/My Disk"), so it must be recovered from
		// the raw line at the ninth column's byte offset, not joined from fields.
		mount := fields[8]
		if idx := nthColumnOffset(line, 9); idx >= 0 {
			mount = strings.TrimSpace(line[idx:])
		}
		fmt.Fprintf(&b, "\n%s — %s total, %s used (%s), %s free", mount, fields[1], fields[2], fields[4], fields[3])
		kept++
	}
	if kept == 0 {
		return strings.TrimSpace(dfOut)
	}
	return b.String()
}

// nthColumnOffset returns the byte offset where the n-th (1-based)
// whitespace-separated column begins in line, or -1 if the line has fewer
// columns. It lets a renderer take "column N through end of line" as one value
// when that final column may itself contain spaces.
func nthColumnOffset(line string, n int) int {
	cols := 0
	for i := 0; i < len(line); {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		cols++
		if cols == n {
			return i
		}
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
	}
	return -1
}

// runSoftwareUpdateCheck asks Apple's update service whether macOS updates are
// pending (`softwareupdate -l`). Read-only: it never downloads or installs —
// installing needs administrator rights and stays an open_settings hand-off.
// The check talks to Apple's servers and typically takes 10–60 seconds; a rare
// slow scan is bounded by the engine's wall-clock timeout, which surfaces as a
// clear "timed out" error the model can relay.
func runSoftwareUpdateCheck(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("softwareupdate")
	if err != nil {
		return "", err
	}
	out, err := runCommand(ctx, bin, "-l")
	if err != nil {
		return "", err
	}
	// A failed check (network down, service unreachable) must surface as a
	// failure, not be formatted as if it were a verdict.
	if err := commandFailed("softwareupdate", out); err != nil {
		return "", err
	}
	return renderSoftwareUpdateCheck(out.Stdout, out.Stderr), nil
}

// renderSoftwareUpdateCheck merges softwareupdate's split output into one
// readable block. The tool writes its progress banner and the common "No new
// software available." verdict to stderr, with any found updates on stdout —
// so both streams matter and neither alone is the answer.
func renderSoftwareUpdateCheck(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	// Drop the noisy progress banner; keep every other stderr line (verdicts and
	// real errors both arrive there).
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "Software Update Tool") || strings.HasPrefix(trimmed, "Finding available software") {
			continue
		}
		kept = append(kept, trimmed)
	}
	parts := nonEmpty(strings.Join(kept, "\n"), stdout)
	if len(parts) == 0 {
		return "softwareupdate returned no output."
	}
	return strings.Join(parts, "\n")
}
