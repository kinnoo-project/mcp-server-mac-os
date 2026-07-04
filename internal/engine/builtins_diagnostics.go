// builtins_diagnostics.go implements the read-only diagnostics builtins that
// answer "what has my Mac been doing and how hard is it working right now?":
//
//   - system_log   — a recent slice of the unified system log (`log show`),
//     optionally narrowed to one process and/or subsystem, for "what is spamming
//     the log / what did app X just report?".
//   - top_processes — a live, real-time %CPU/%MEM snapshot from `top`, which
//     complements the process domain's ps-based list_processes (ps reports an
//     averaged, less immediate %CPU) with an instantaneous sample.
//   - thermal_state — whether the CPU is currently being throttled to manage
//     heat (`pmset -g therm`), for "why is my Mac suddenly slow / hot?".
//
// Only system_log takes model-controlled free text. Its two optional filters
// (process, subsystem) are NEVER passed to the binary as raw operands: they are
// validated against strict charsets and then composed, Go-side, into a single
// `log` predicate expression whose surrounding quotes the values provably cannot
// escape (composeLogPredicate rejects the quote and backslash characters that
// would be needed to break out). top_processes and thermal_state take no
// free-text input at all — their argv is entirely fixed constants plus clamped
// integers and closed enums — so they carry no injection surface.
//
// powermetrics, the tool that would give per-process GPU/energy and finer
// thermal detail, is deliberately NOT wired up here: it refuses to run without
// root, and this server never escalates privileges. `top` + `pmset -g therm`
// cover the non-privileged subset; see docs/issues/note-v4-diagnostics-design.md.
package engine

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// logSubsystemPattern is the allowlist a model-supplied `subsystem` filter must
// match in full. Subsystems are reverse-DNS identifiers (e.g. "com.apple.wifi"),
// so only ASCII letters, digits, dot and hyphen are permitted, and the value must
// start with an alphanumeric. This excludes the quote/backslash/space/predicate
// metacharacters that could otherwise alter the composed predicate expression.
var logSubsystemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)

// validateLogProcessValue vets the `process` filter for the unified-log query.
// A process name can legitimately contain spaces ("Google Chrome"), so its
// charset is broader than the subsystem's — but the value is about to be embedded
// inside a `process == "<value>"` predicate string, so anything that could close
// that string or inject predicate syntax is rejected: the double-quote and
// backslash characters, control characters (including newlines), and a leading
// dash (belt-and-braces, so the value can never be mistaken for an option even
// though it rides inside quotes). It returns the trimmed value.
func validateLogProcessValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("process filter must not be empty")
	}
	if len(value) > 128 {
		return "", fmt.Errorf("process filter %q is too long (max 128 characters)", value)
	}
	if value[0] == '-' {
		return "", fmt.Errorf("process filter %q must not begin with '-'", value)
	}
	for _, r := range value {
		switch {
		case r < 0x20 || r == 0x7f:
			return "", fmt.Errorf("process filter must not contain control characters")
		case r == '"' || r == '\\':
			return "", fmt.Errorf("process filter must not contain quote or backslash characters")
		}
	}
	return value, nil
}

// composeLogPredicate turns the two optional, model-controlled filter values into
// a single `log` predicate expression, or "" when neither is supplied (meaning
// "show everything in the window"). It is the security-critical seam for
// system_log: the predicate is built ENTIRELY from constant operators and
// validated values — the raw model string is never handed to the binary — so a
// hostile value can neither escape the quoted string it sits in nor be parsed by
// `log` as one of its own flags. It is a pure function so the accept/reject table
// can be exercised directly in tests.
func composeLogPredicate(process, subsystem string) (string, error) {
	var clauses []string
	if strings.TrimSpace(process) != "" {
		p, err := validateLogProcessValue(process)
		if err != nil {
			return "", err
		}
		clauses = append(clauses, fmt.Sprintf(`process == "%s"`, p))
	}
	if strings.TrimSpace(subsystem) != "" {
		s := strings.TrimSpace(subsystem)
		if len(s) > 128 {
			return "", fmt.Errorf("subsystem filter %q is too long (max 128 characters)", s)
		}
		if !logSubsystemPattern.MatchString(s) {
			return "", fmt.Errorf("subsystem filter %q is not a valid reverse-DNS identifier (letters, digits, '.', '-' only)", s)
		}
		clauses = append(clauses, fmt.Sprintf(`subsystem == "%s"`, s))
	}
	return strings.Join(clauses, " && "), nil
}

// runSystemLog returns a recent slice of the unified system log. The time window
// is a clamped integer (1–30 minutes) and the optional process/subsystem filters
// are composed into a validated predicate — so every argv element is either a
// fixed constant, a bounded integer, or a predicate string the model cannot
// subvert. Output is passed through the shared 32 KB compaction because an
// unfiltered window can be very large; the header tells the model how to narrow
// it (by process/subsystem) when it is truncated.
func runSystemLog(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	minutes := 5
	if n, ok := getInt(in, "duration_minutes"); ok {
		minutes = n
	}
	minutes = clampInt(minutes, 1, 30)

	process, _ := getString(in, "process")
	subsystem, _ := getString(in, "subsystem")
	predicate, err := composeLogPredicate(process, subsystem)
	if err != nil {
		return "", err
	}

	bin, err := policy.ResolveBinary("log")
	if err != nil {
		return "", err
	}
	// --style syslog gives the compact, familiar one-line-per-entry format;
	// --last <N>m bounds the window. Default-level entries only (no --info/
	// --debug), which keeps the volume sane for an LLM context.
	args := []string{"show", "--style", "syslog", "--last", strconv.Itoa(minutes) + "m"}
	if predicate != "" {
		args = append(args, "--predicate", predicate)
	}
	res, err := runCommand(ctx, bin, args...)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		out = strings.TrimSpace(res.Stderr)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Unified system log — last %d minute(s)", minutes)
	if predicate != "" {
		fmt.Fprintf(&b, " (filter: %s)", predicate)
	}
	b.WriteString(":\n\n")
	if out == "" {
		b.WriteString("(no matching log entries in this window)")
		return b.String(), nil
	}
	b.WriteString(out)
	return compactOutput(b.String()), nil
}

// topRow is one parsed process line from a `top` sample: the pid, the
// instantaneous %CPU and memory footprint (kept as the strings top prints, e.g.
// "12.3" and "456M", since they are for display only), and the command name
// (which top prints last and which may contain spaces).
type topRow struct {
	pid     string
	cpu     string
	mem     string
	command string
}

// runTopProcesses reports a live %CPU/%MEM snapshot. It asks `top` for TWO
// samples (`-l 2`) and reads only the second: top's first sample reports CPU
// usage accumulated since boot (a meaningless average), whereas the second
// reflects the real interval between the two samples. Command is requested LAST
// in the -stats list so that, exactly as with ps, the fixed-width leading columns
// are single tokens and the space-bearing command is simply everything after
// them — no brittle column-offset parsing required. Every argv element is a
// constant, a clamped integer, or a closed enum, so there is no injection surface.
func runTopProcesses(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	limit := 15
	if n, ok := getInt(in, "limit"); ok {
		limit = n
	}
	limit = clampInt(limit, 1, 50)

	// Map the user-facing enum to top's sort key. top calls the memory column
	// "mem"; anything other than "memory" sorts by CPU, matching the manifest.
	sortBy, _ := getString(in, "sort_by")
	sortKey := "cpu"
	if sortBy == "memory" {
		sortKey = "mem"
	}

	bin, err := policy.ResolveBinary("top")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "-l", "2", "-n", strconv.Itoa(limit), "-o", sortKey, "-stats", "pid,cpu,mem,command")
	if err != nil {
		return "", err
	}
	rows := parseTopSample(res.Stdout)
	if len(rows) == 0 {
		return "No process rows were returned by top.", nil
	}

	metric := "CPU"
	if sortKey == "mem" {
		metric = "memory"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Top %d process(es) by %s (live sample):\n\n", len(rows), metric)
	fmt.Fprintf(&b, "%-7s %7s %10s  %s\n", "PID", "%CPU", "MEM", "COMMAND")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-7s %7s %10s  %s\n", r.pid, r.cpu, r.mem, truncateCommand(r.command, 60))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// parseTopSample extracts the process rows from the SECOND sample of `top -l 2`
// output (`-stats pid,cpu,mem,command`). It first locates the last "Processes:"
// banner — top prints one per sample, so the last one begins the interval-based
// sample we want — then reads the table that follows its "PID … COMMAND" header.
// Each data row's first field is the numeric pid; cpu and mem are the next two
// tokens; the command is the remainder (top prints it last and it may contain
// spaces). Malformed or non-numeric-pid lines are skipped.
func parseTopSample(stdout string) []topRow {
	lines := strings.Split(stdout, "\n")

	// Find the start of the last sample.
	sampleStart := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "Processes:") {
			sampleStart = i
		}
	}

	// Within that sample, find the process-table header row.
	headerIdx := -1
	for i := sampleStart; i < len(lines); i++ {
		f := strings.Fields(lines[i])
		if len(f) >= 2 && f[0] == "PID" && strings.Contains(lines[i], "COMMAND") {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 {
		return nil
	}

	var rows []topRow
	for _, line := range lines[headerIdx+1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue // header repeats, banners, or blank lines
		}
		rows = append(rows, topRow{
			pid:     fields[0],
			cpu:     fields[1],
			mem:     fields[2],
			command: strings.Join(fields[3:], " "),
		})
	}
	return rows
}

// runThermalState reports whether the CPU is currently being throttled to manage
// heat, read from `pmset -g therm` (no admin rights required). The invocation is
// a fixed argv with no model input, so there is no injection surface.
func runThermalState(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("pmset")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "-g", "therm")
	if err != nil {
		return "", err
	}
	return renderThermalState(res.Stdout), nil
}

// thermSpeedLimitRe captures the CPU_Speed_Limit percentage from `pmset -g therm`
// output, e.g. "CPU_Speed_Limit    = 100". A value below 100 means macOS is
// clamping CPU speed to shed heat.
var thermSpeedLimitRe = regexp.MustCompile(`CPU_Speed_Limit\s*=\s*(\d+)`)

// renderThermalState is the pure formatting half of runThermalState, split out so
// the interpretation can be unit-tested against canned pmset output. It leads with
// a plain-language verdict — throttled (and to what percentage) versus running at
// full speed — and then includes pmset's raw lines for completeness.
func renderThermalState(stdout string) string {
	out := strings.TrimSpace(stdout)
	if out == "" {
		return "No thermal information was returned by pmset."
	}

	// Always resolve to exactly one plain-language verdict so the function's
	// contract ("leads with a verdict") holds even when pmset's wording changes:
	// prefer the parsed CPU_Speed_Limit, fall back to the "no warning recorded"
	// phrasing, and finally to an explicit "no indicator found" line rather than
	// silently emitting only the raw output.
	verdict := "No explicit throttling indicator was reported; see the raw pmset output below."
	if m := thermSpeedLimitRe.FindStringSubmatch(out); m != nil {
		if limit, err := strconv.Atoi(m[1]); err == nil {
			if limit < 100 {
				verdict = fmt.Sprintf("CPU speed is currently throttled to %d%% of maximum to manage heat.", limit)
			} else {
				verdict = "CPU is running at full speed — no thermal throttling."
			}
		}
	} else if strings.Contains(out, "No thermal warning level") {
		verdict = "No thermal pressure has been recorded — the CPU is not being throttled."
	}

	var b strings.Builder
	b.WriteString("Thermal state:\n")
	b.WriteString(verdict)
	b.WriteString("\n\nRaw pmset output:\n")
	b.WriteString(out)
	return b.String()
}
