// builtins_process.go implements the read-only process- and
// resource-monitoring builtins: a ranked snapshot of running processes
// (list_processes), a deep dive on one process by PID (process_info), system
// CPU pressure (cpu_load), system memory breakdown (memory_stats), whole-device
// GPU utilization (gpu_stats), and the launchd-managed auto-start inventory
// (startup_items). Together they answer the everyday "what is running, what is
// it costing me, and what starts on its own" questions a macOS command centre
// needs.
//
// All six are pure reads over stable system utilities — ps, sysctl, vm_stat,
// ioreg, launchctl — parsed into concise, human-readable summaries in Go. None
// of them take a model-controlled string that could reach a binary as a flag:
// the only model inputs are integers (a PID, a row limit) and closed enums
// (sort order, scope), plus list_processes' free-text `filter`, which is matched
// entirely in-process and is NEVER passed to a subprocess. PID inputs are
// validated to be > 1 so they cannot be read as options and so the protected low
// PIDs (the kernel and launchd) are never targeted by the mutating siblings in
// mutate_process.go that share these helpers.
package engine

import (
	"context"
	"fmt"
	"os/user"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// procRow is one parsed line of `ps` output: the identity (pid/ppid/user), the
// resource shares (pcpu/pmem are percentages as ps reports them), how long the
// process has run (etime), its scheduler state, and the executable path that
// `comm` prints. comm is the full path on macOS (e.g.
// "/Applications/Safari.app/Contents/MacOS/Safari"), which both names the
// responsible binary and lets classifyOrigin tell a system binary from a
// user-installed app.
type procRow struct {
	pid     int
	ppid    int
	user    string
	pcpu    float64
	pmem    float64
	etime   string
	state   string
	command string
}

// psSnapshotColumns is the fixed column set list_processes/topProcesses request
// from ps. Every column except the trailing comm is a single whitespace-free
// token, so the path-bearing comm can safely be everything after the seventh
// field even when it contains spaces (e.g. "/Applications/Google Chrome.app/…").
var psSnapshotColumns = "pid,ppid,user,pcpu,pmem,etime,state,comm"

// fetchProcesses runs a single `ps` over every process and parses the result.
// One call backs list_processes, cpu_load, and memory_stats so the snapshot is
// internally consistent and cheap.
func fetchProcesses(ctx context.Context) ([]procRow, error) {
	bin, err := policy.ResolveBinary("ps")
	if err != nil {
		return nil, err
	}
	res, err := runCommand(ctx, bin, "-Axo", psSnapshotColumns)
	if err != nil {
		return nil, err
	}
	return parsePsRows(res.Stdout), nil
}

// parsePsRows turns `ps -Axo pid,ppid,user,pcpu,pmem,etime,state,comm` output
// into rows. The header line and any malformed row are skipped. Because comm is
// the last column and may contain spaces, the first seven fields are taken
// positionally and everything after them is rejoined as the command path.
func parsePsRows(stdout string) []procRow {
	var rows []procRow
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[0] == "PID" {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		pcpu, _ := strconv.ParseFloat(fields[3], 64)
		pmem, _ := strconv.ParseFloat(fields[4], 64)
		rows = append(rows, procRow{
			pid:     pid,
			ppid:    ppid,
			user:    fields[2],
			pcpu:    pcpu,
			pmem:    pmem,
			etime:   fields[5],
			state:   fields[6],
			command: strings.Join(fields[7:], " "),
		})
	}
	return rows
}

// sortProcs orders rows in place, largest first, by the chosen metric. Any value
// other than "memory" sorts by CPU, matching the manifest default.
func sortProcs(rows []procRow, by string) {
	sort.SliceStable(rows, func(i, j int) bool {
		if by == "memory" {
			return rows[i].pmem > rows[j].pmem
		}
		return rows[i].pcpu > rows[j].pcpu
	})
}

// topProcesses returns the n highest rows by the chosen metric. n is clamped to
// at least 1; a non-positive or over-large n is the caller's job to bound first.
func topProcesses(rows []procRow, by string, n int) []procRow {
	sorted := make([]procRow, len(rows))
	copy(sorted, rows)
	sortProcs(sorted, by)
	if n < 1 {
		n = 1
	}
	if n > len(sorted) {
		n = len(sorted)
	}
	return sorted[:n]
}

// classifyOrigin labels an executable path as "system" (an Apple/OS binary),
// "user" (an app or tool the user installed), or "other" when neither pattern
// fits. It is a path heuristic, not a code-signature check, but it reliably
// answers the practical question "is this something Apple ships or something I
// added" that the monitor surfaces.
func classifyOrigin(exePath string) string {
	switch {
	case exePath == "":
		return "other"
	case strings.HasPrefix(exePath, "/System/"),
		strings.HasPrefix(exePath, "/usr/libexec/"),
		strings.HasPrefix(exePath, "/usr/sbin/"),
		strings.HasPrefix(exePath, "/usr/bin/"),
		strings.HasPrefix(exePath, "/sbin/"),
		strings.HasPrefix(exePath, "/bin/"),
		strings.HasPrefix(exePath, "/Library/Apple/"):
		return "system"
	case strings.HasPrefix(exePath, "/Applications/"),
		strings.HasPrefix(exePath, "/Users/"),
		strings.HasPrefix(exePath, "/usr/local/"),
		strings.HasPrefix(exePath, "/opt/"):
		return "user"
	default:
		return "other"
	}
}

// appBundleRe captures the application name from an executable path that lives
// inside a .app bundle, e.g. "/Applications/Google Chrome.app/Contents/MacOS/…"
// yields "Google Chrome". It anchors on the LAST ".app/" so a nested helper app
// resolves to its own bundle.
var appBundleRe = regexp.MustCompile(`([^/]+)\.app/`)

// appNameFromExePath returns the owning application's name when the executable
// sits inside a .app bundle, or "" for a plain binary (a daemon or CLI tool).
// quit_process uses this to decide whether a PID belongs to a GUI app it can
// gracefully quit via AppleScript.
func appNameFromExePath(path string) string {
	matches := appBundleRe.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		return ""
	}
	// The last match is the innermost bundle that actually contains the binary.
	return matches[len(matches)-1][1]
}

// humanBytes renders a byte count in the largest unit that keeps the number
// readable (e.g. 17179869184 -> "16.0 GB"), so memory figures are legible
// without the caller doing arithmetic.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// stateMeaning expands ps's terse scheduler-state code into a short phrase. Only
// the leading character is significant (ps appends flags like 's'/'+'); the most
// important call-out is 'Z', a zombie/defunct process the user may be hunting.
func stateMeaning(state string) string {
	if state == "" {
		return "unknown"
	}
	switch state[0] {
	case 'Z':
		return "zombie (defunct — waiting for its parent to reap it)"
	case 'R':
		return "running"
	case 'S':
		return "sleeping (idle, waiting for an event)"
	case 'I':
		return "idle (sleeping more than ~20s)"
	case 'U':
		return "uninterruptible wait (usually disk/IO)"
	case 'T':
		return "stopped"
	default:
		return "state " + state
	}
}

// truncateCommand bounds a command string so a single pathological argv cannot
// blow the per-row width (and the overall output budget) out.
func truncateCommand(cmd string, max int) string {
	if len(cmd) <= max {
		return cmd
	}
	return cmd[:max-1] + "…"
}

// validateProcessFilter rejects a list_processes filter that contains control
// characters. The filter never reaches a subprocess (matching is done in Go), so
// this is only about keeping the substring sane and the output uncorrupted.
func validateProcessFilter(filter string) error {
	for _, r := range filter {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("filter must not contain control characters")
		}
	}
	return nil
}

// clampInt bounds v to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// runListProcesses produces the ranked process table. It fetches one snapshot,
// then applies scope (all vs. the current user), the optional in-process
// substring filter, the chosen sort, and the row limit — in that order — so the
// limit counts only rows the user actually asked to see.
func runListProcesses(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	rows, err := fetchProcesses(ctx)
	if err != nil {
		return "", err
	}

	sortBy, _ := getString(in, "sort_by")
	if sortBy == "" {
		sortBy = "cpu"
	}
	scope, _ := getString(in, "scope")
	filter, _ := getString(in, "filter")
	if err := validateProcessFilter(filter); err != nil {
		return "", err
	}
	limit := 20
	if n, ok := getInt(in, "limit"); ok {
		limit = n
	}
	limit = clampInt(limit, 1, 100)

	if scope == "user" {
		if u, err := user.Current(); err == nil {
			rows = filterByUser(rows, u.Username)
		}
	}
	if filter != "" {
		rows = filterByCommand(rows, filter)
	}

	total := len(rows)
	sortProcs(rows, sortBy)
	if len(rows) > limit {
		rows = rows[:limit]
	}

	metric := "CPU"
	if sortBy == "memory" {
		metric = "memory"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Showing %d of %d process(es), ranked by %s:\n\n", len(rows), total, metric)
	fmt.Fprintf(&b, "%-7s %6s %6s %-7s %-12s %-14s %s\n", "PID", "%CPU", "%MEM", "ORIGIN", "USER", "ELAPSED", "COMMAND")
	for _, p := range rows {
		marker := ""
		if p.state != "" && p.state[0] == 'Z' {
			marker = " [zombie]"
		}
		fmt.Fprintf(&b, "%-7d %6.1f %6.1f %-7s %-12s %-14s %s%s\n",
			p.pid, p.pcpu, p.pmem, classifyOrigin(p.command), truncateField(p.user, 12), p.etime,
			truncateCommand(p.command, 80), marker)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// filterByUser keeps only rows owned by username (ps prints the short login
// name, which is what user.Current().Username holds on macOS).
func filterByUser(rows []procRow, username string) []procRow {
	var out []procRow
	for _, p := range rows {
		if p.user == username {
			out = append(out, p)
		}
	}
	return out
}

// filterByCommand keeps rows whose command contains sub, case-insensitively.
func filterByCommand(rows []procRow, sub string) []procRow {
	needle := strings.ToLower(sub)
	var out []procRow
	for _, p := range rows {
		if strings.Contains(strings.ToLower(p.command), needle) {
			out = append(out, p)
		}
	}
	return out
}

// truncateField bounds a fixed-width column value so one long username cannot
// break the table alignment.
func truncateField(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// runProcessInfo reports everything known about a single PID. It is several
// small ps reads (stable single-token fields, then the exe path, the full argv,
// and the parent's name) plus a launchd cross-reference, composed into one
// block. A PID with no matching process is reported as data, not an error.
func runProcessInfo(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	pid, ok := getInt(in, "pid")
	if !ok {
		return "", fmt.Errorf("process_info: 'pid' is required")
	}
	if pid <= 1 {
		return "", fmt.Errorf("process_info: 'pid' must be greater than 1 (got %d)", pid)
	}
	bin, err := policy.ResolveBinary("ps")
	if err != nil {
		return "", err
	}
	pidStr := strconv.Itoa(pid)

	// Single-token fields plus lstart (which is the trailing, space-bearing
	// column). The "=" suffix on each column suppresses ps's header line.
	res, err := runCommand(ctx, bin, "-o", "pid=,ppid=,user=,pcpu=,pmem=,etime=,state=,lstart=", "-p", pidStr)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(res.Stdout)
	if len(fields) < 8 {
		return fmt.Sprintf("No process with PID %d is currently running.", pid), nil
	}
	ppid, _ := strconv.Atoi(fields[1])
	lstart := strings.Join(fields[7:], " ")

	exePath := firstLine(mustRun(ctx, bin, "-o", "comm=", "-p", pidStr))
	fullCmd := firstLine(mustRun(ctx, bin, "-o", "command=", "-p", pidStr))
	parentName := "(unknown)"
	if ppid > 0 {
		if pn := firstLine(mustRun(ctx, bin, "-o", "comm=", "-p", strconv.Itoa(ppid))); pn != "" {
			parentName = pn
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Process %d\n", pid)
	fmt.Fprintf(&b, "  Command:        %s\n", fullCmd)
	fmt.Fprintf(&b, "  Executable:     %s\n", exePath)
	fmt.Fprintf(&b, "  Origin:         %s\n", originDescription(exePath))
	fmt.Fprintf(&b, "  Owner:          %s\n", fields[2])
	fmt.Fprintf(&b, "  Parent:         PID %d (%s)\n", ppid, parentName)
	fmt.Fprintf(&b, "  CPU / Memory:   %s%% CPU, %s%% memory\n", fields[3], fields[4])
	fmt.Fprintf(&b, "  Started:        %s (running %s)\n", lstart, fields[5])
	fmt.Fprintf(&b, "  State:          %s\n", stateMeaning(fields[6]))
	fmt.Fprintf(&b, "  Auto-started:   %s\n", launchdStatus(ctx, pid))
	return strings.TrimRight(b.String(), "\n"), nil
}

// originDescription pairs classifyOrigin's label with a short gloss for the
// single-process view.
func originDescription(exePath string) string {
	switch classifyOrigin(exePath) {
	case "system":
		return "system (an Apple/macOS binary)"
	case "user":
		if app := appNameFromExePath(exePath); app != "" {
			return fmt.Sprintf("user-installed (the %q application)", app)
		}
		return "user-installed (a program added to this Mac)"
	default:
		return "other / unclassified"
	}
}

// launchdStatus says whether launchd currently manages the PID (i.e. the process
// is one of macOS's auto-started agents/daemons). It is best-effort: an
// unreadable listing yields "could not determine".
func launchdStatus(ctx context.Context, pid int) string {
	bin, err := policy.ResolveBinary("launchctl")
	if err != nil {
		return "could not determine"
	}
	res, err := runCommand(ctx, bin, "list")
	if err != nil {
		return "could not determine"
	}
	if label, ok := launchdLabelForPID(res.Stdout, pid); ok {
		return fmt.Sprintf("yes — managed by launchd as %q", label)
	}
	return "not directly managed by launchd (may have been launched on demand or by a user)"
}

// launchdLabelForPID scans `launchctl list` output for the row whose PID column
// equals pid and returns its label. The output is a tab-separated
// "PID\tStatus\tLabel" table whose PID column is "-" for loaded-but-not-running
// jobs.
func launchdLabelForPID(stdout string, pid int) (string, bool) {
	want := strconv.Itoa(pid)
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "PID" {
			continue
		}
		if fields[0] == want {
			return fields[len(fields)-1], true
		}
	}
	return "", false
}

// runCpuLoad reports system-wide CPU pressure: load averages (raw and per-core)
// and the top CPU consumers. Per-core load is the meaningful figure — a load of
// 8 is busy on an 8-core machine but overloaded on a 4-core one.
func runCpuLoad(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("sysctl")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "-n", "vm.loadavg", "hw.ncpu")
	if err != nil {
		return "", err
	}
	load1, load5, load15, ncpu := parseLoadAvg(res.Stdout)

	limit := 5
	if n, ok := getInt(in, "limit"); ok {
		limit = n
	}
	limit = clampInt(limit, 1, 20)

	rows, err := fetchProcesses(ctx)
	if err != nil {
		return "", err
	}
	top := topProcesses(rows, "cpu", limit)

	var b strings.Builder
	fmt.Fprintf(&b, "CPU cores: %d\n", ncpu)
	fmt.Fprintf(&b, "Load average: %.2f (1m), %.2f (5m), %.2f (15m)\n", load1, load5, load15)
	if ncpu > 0 {
		fmt.Fprintf(&b, "Per-core load: %.2f (1m), %.2f (5m), %.2f (15m)  — ~1.0 per core means fully busy\n",
			load1/float64(ncpu), load5/float64(ncpu), load15/float64(ncpu))
	}
	b.WriteString("\nTop CPU consumers:\n")
	for _, p := range top {
		fmt.Fprintf(&b, "  %6.1f%%  pid %-7d %s\n", p.pcpu, p.pid, truncateCommand(p.command, 70))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// parseLoadAvg pulls the three load averages and core count from the combined
// `sysctl -n vm.loadavg hw.ncpu` output, which looks like:
//
//	{ 2.88 2.10 1.83 }
//	10
func parseLoadAvg(stdout string) (l1, l5, l15 float64, ncpu int) {
	cleaned := strings.NewReplacer("{", " ", "}", " ").Replace(stdout)
	nums := strings.Fields(cleaned)
	if len(nums) >= 3 {
		l1, _ = strconv.ParseFloat(nums[0], 64)
		l5, _ = strconv.ParseFloat(nums[1], 64)
		l15, _ = strconv.ParseFloat(nums[2], 64)
	}
	if len(nums) >= 4 {
		ncpu, _ = strconv.Atoi(nums[3])
	}
	return l1, l5, l15, ncpu
}

// runMemoryStats reports total RAM and how it is apportioned (free, wired,
// active, inactive, compressed) plus the top memory consumers. Totals come from
// hw.memsize; the breakdown is vm_stat's page counts scaled by its page size.
func runMemoryStats(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	sysctlBin, err := policy.ResolveBinary("sysctl")
	if err != nil {
		return "", err
	}
	memRes, err := runCommand(ctx, sysctlBin, "-n", "hw.memsize")
	if err != nil {
		return "", err
	}
	total, _ := strconv.ParseUint(strings.TrimSpace(firstLine(memRes.Stdout)), 10, 64)

	vmBin, err := policy.ResolveBinary("vm_stat")
	if err != nil {
		return "", err
	}
	// Plain `vm_stat` prints the "Pages free: N." key/value report parseVMStat
	// reads. (The sampling form `vm_stat <interval>` prints a different, columnar
	// layout, so it is deliberately not used here.)
	vmRes, err := runCommand(ctx, vmBin)
	if err != nil {
		return "", err
	}
	m := parseVMStat(vmRes.Stdout)

	free := m["free"] + m["speculative"]
	wired := m["wired down"]
	active := m["active"]
	inactive := m["inactive"]
	compressed := m["occupied by compressor"]
	used := uint64(0)
	if total > free {
		used = total - free
	}

	limit := 5
	if n, ok := getInt(in, "limit"); ok {
		limit = n
	}
	limit = clampInt(limit, 1, 20)
	rows, err := fetchProcesses(ctx)
	if err != nil {
		return "", err
	}
	top := topProcesses(rows, "memory", limit)

	var b strings.Builder
	fmt.Fprintf(&b, "Total RAM:   %s\n", humanBytes(total))
	fmt.Fprintf(&b, "Used:        %s\n", humanBytes(used))
	fmt.Fprintf(&b, "Free:        %s\n", humanBytes(free))
	fmt.Fprintf(&b, "  Wired:      %s (kernel/unswappable)\n", humanBytes(wired))
	fmt.Fprintf(&b, "  Active:     %s\n", humanBytes(active))
	fmt.Fprintf(&b, "  Inactive:   %s\n", humanBytes(inactive))
	fmt.Fprintf(&b, "  Compressed: %s\n", humanBytes(compressed))
	b.WriteString("\nTop memory consumers:\n")
	for _, p := range top {
		fmt.Fprintf(&b, "  %6.1f%%  pid %-7d %s\n", p.pmem, p.pid, truncateCommand(p.command, 70))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// vmStatLineRe matches one vm_stat data line, capturing the metric name and its
// page count, e.g. "Pages free:                                9168." The
// trailing period is excluded by the digit-only capture.
var vmStatLineRe = regexp.MustCompile(`^Pages? (.+?):\s+(\d+)\.?$`)

// vmStatPageSizeRe captures the page size from vm_stat's header line, e.g.
// "Mach Virtual Memory Statistics: (page size of 16384 bytes)".
var vmStatPageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)

// parseVMStat converts vm_stat output into a map of metric name -> bytes. Page
// counts are multiplied by the reported page size so callers work in bytes. Keys
// are the metric text after "Pages " (e.g. "free", "wired down",
// "occupied by compressor").
func parseVMStat(stdout string) map[string]uint64 {
	pageSize := uint64(4096)
	if m := vmStatPageSizeRe.FindStringSubmatch(stdout); m != nil {
		if ps, err := strconv.ParseUint(m[1], 10, 64); err == nil && ps > 0 {
			pageSize = ps
		}
	}
	out := make(map[string]uint64)
	for _, line := range strings.Split(stdout, "\n") {
		m := vmStatLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		count, err := strconv.ParseUint(m[2], 10, 64)
		if err != nil {
			continue
		}
		out[m[1]] = count * pageSize
	}
	return out
}

// runGPUStats reports whole-device GPU utilization from IOKit's accelerator
// performance counters. macOS exposes these without admin rights, but ONLY as a
// device-wide figure — per-process GPU usage requires powermetrics, which
// refuses to run as a non-superuser — so the summary says so explicitly to set
// the right expectation.
func runGPUStats(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("ioreg")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator")
	if err != nil {
		return "", err
	}
	stats := parseGPUStats(res.Stdout)
	if len(stats) == 0 {
		return "No GPU accelerator statistics were available from IOKit on this Mac.", nil
	}

	var b strings.Builder
	b.WriteString("GPU utilization (whole device):\n")
	if v, ok := stats["Device Utilization %"]; ok {
		fmt.Fprintf(&b, "  Device:   %d%%\n", v)
	}
	if v, ok := stats["Renderer Utilization %"]; ok {
		fmt.Fprintf(&b, "  Renderer: %d%%\n", v)
	}
	if v, ok := stats["Tiler Utilization %"]; ok {
		fmt.Fprintf(&b, "  Tiler:    %d%%\n", v)
	}
	if v, ok := stats["In use system memory"]; ok {
		fmt.Fprintf(&b, "  GPU memory in use: %s\n", humanBytes(uint64(v)))
	}
	b.WriteString("\nNote: macOS does not expose per-process GPU usage without administrator rights, so the figures above are for the whole GPU. To guess which app is driving it, look for GPU-helper processes (e.g. WindowServer, *.WebKit.GPU) high in list_processes.")
	return b.String(), nil
}

// parseGPUStats extracts the numeric fields of interest from the
// "PerformanceStatistics" dictionary in ioreg output. ioreg prints the dict on
// one line as "key"=value pairs, so each field is pulled with a targeted regex
// rather than a full plist parse.
func parseGPUStats(stdout string) map[string]int {
	keys := []string{
		"Device Utilization %",
		"Renderer Utilization %",
		"Tiler Utilization %",
		"In use system memory",
	}
	out := make(map[string]int)
	for _, k := range keys {
		re := regexp.MustCompile(regexp.QuoteMeta(`"`+k+`"`) + `=(\d+)`)
		if m := re.FindStringSubmatch(stdout); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil {
				out[k] = v
			}
		}
	}
	return out
}

// runStartupItems lists launchd-managed auto-start items. The default scope
// hides Apple's own com.apple.* entries so the user sees what THEY installed
// that starts on its own; "all" shows everything.
func runStartupItems(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("launchctl")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "list")
	if err != nil {
		return "", err
	}

	scope, _ := getString(in, "scope")
	if scope == "" {
		scope = "third_party"
	}
	limit := 50
	if n, ok := getInt(in, "limit"); ok {
		limit = n
	}
	limit = clampInt(limit, 1, 200)

	items := parseLaunchctlList(res.Stdout, scope == "third_party")
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}

	label := "third-party"
	if scope == "all" {
		label = "all"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s launchd item(s) (showing %d):\n\n", total, label, len(items))
	fmt.Fprintf(&b, "%-8s %-10s %s\n", "PID", "STATUS", "LABEL")
	for _, it := range items {
		fmt.Fprintf(&b, "%-8s %-10s %s\n", it.pid, it.status, it.label)
	}
	b.WriteString("\nNote: classic Login Items are not shown here (reading them needs an Automation permission grant).")
	return strings.TrimRight(b.String(), "\n"), nil
}

// launchdItem is one row of `launchctl list`: the running PID ("-" if not
// running), the last exit status, and the job label.
type launchdItem struct {
	pid    string
	status string
	label  string
}

// parseLaunchctlList parses `launchctl list` output into rows, optionally
// dropping Apple's com.apple.* labels when thirdPartyOnly is set.
func parseLaunchctlList(stdout string, thirdPartyOnly bool) []launchdItem {
	var out []launchdItem
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "PID" {
			continue
		}
		label := fields[len(fields)-1]
		if thirdPartyOnly && strings.HasPrefix(label, "com.apple.") {
			continue
		}
		out = append(out, launchdItem{pid: fields[0], status: fields[1], label: label})
	}
	return out
}

// firstLine returns the first non-empty trimmed line of s, or "".
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// mustRun runs a resolved binary and returns its stdout, swallowing errors as an
// empty string. It is used only for the supplementary process_info reads (exe
// path, full argv, parent name) where a missing field should degrade gracefully
// rather than fail the whole report.
func mustRun(ctx context.Context, bin string, args ...string) string {
	res, err := runCommand(ctx, bin, args...)
	if err != nil {
		return ""
	}
	return res.Stdout
}
