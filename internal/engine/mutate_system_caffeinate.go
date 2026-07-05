// mutate_system_caffeinate.go implements the `system` domain's keep-awake pair:
// keep_awake (start a timed session that stops this Mac from idle-sleeping) and
// allow_sleep (end any such session so normal sleep resumes). They sit on the
// mutation seam alongside the other power controls (mutate_system_power.go)
// because both change the machine's sleep behaviour.
//
// # Why they are two operations, not one reversible operation
//
// keep_awake runs `caffeinate`, which must keep running for the whole requested
// duration — long after the tool call that started it returns. So its forward
// command is DETACHED (Command.Detach; see executor.go): the process is launched
// into the background and its PID is only known AFTER it starts. A staged
// inverse, by contrast, must be fully resolved at STAGE time (mutate.go). Those
// two facts cannot both hold, so keep_awake carries no inverse and is classified
// irreversible; the way to end a session early is the separate allow_sleep
// operation (or simply letting caffeinate's own `-t` timeout expire it).
//
// allow_sleep is the canceller: it finds the caffeinate processes THIS user is
// running and sends them SIGTERM. It is auto-commit and low-risk because letting
// a Mac sleep normally again is benign and the change is easy to reissue
// (keep_awake) — mirroring how display_sleep auto-commits.
//
// # Why the inputs are safe
//
// keep_awake's only model input is an integer duration, validated to a bounded
// range and rendered as a decimal argv token, so it can never be read as a flag.
// allow_sleep takes no model input at all: the PIDs it signals come from the
// system's own process table, filtered to processes literally named `caffeinate`
// and owned by the current user — never a model-supplied value — so there is no
// injection surface and no way to aim it at an unrelated process.
package engine

import (
	"context"
	"fmt"
	"os/user"
	"path/filepath"
	"strconv"

	"mcp-server-mac-os/internal/registry"
)

// keepAwake duration bounds. The floor keeps a session meaningful (a sub-minute
// keep-awake is not worth a confirmation round-trip); the ceiling caps it at
// four hours so a forgotten session cannot hold the Mac awake indefinitely — a
// battery-drain/thermal hazard. A caller who wants "all day" should reissue.
const (
	keepAwakeMinSeconds     = 60
	keepAwakeMaxSeconds     = 14400 // 4 hours
	keepAwakeDefaultSeconds = 3600  // 1 hour, the natural "keep it awake for a while"
)

// stageKeepAwake stages a timed keep-awake session. It validates the requested
// duration, then builds a DETACHED `caffeinate -d -i -t <seconds>` as the
// forward command (-d blocks display sleep, -i blocks idle system sleep, -t
// self-expires the session). There is no inverse — the PID is not knowable until
// the detached process starts, and a staged inverse must be resolved at stage
// time — so the plan is irreversible and the preview points the user at
// allow_sleep to end it sooner.
func stageKeepAwake(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	seconds := keepAwakeDefaultSeconds
	if n, ok := getInt(in, "duration_seconds"); ok {
		seconds = n
	}
	if seconds < keepAwakeMinSeconds || seconds > keepAwakeMaxSeconds {
		return nil, fmt.Errorf(
			"keep_awake: duration_seconds must be between %d and %d (got %d)",
			keepAwakeMinSeconds, keepAwakeMaxSeconds, seconds)
	}

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Keep this Mac awake (block display sleep and idle system sleep) for %s. "+
				"It stops on its own when the time is up; to end it sooner, run allow_sleep. "+
				"This does not auto-undo.",
			humanizeSeconds(seconds)),
		// Detached: caffeinate must keep running for the full duration, well after
		// this commit call returns, so it cannot be bound to the request context.
		Forward: Command{Binary: "caffeinate", Args: []string{"-d", "-i", "-t", strconv.Itoa(seconds)}, Detach: true},
		Inverse: nil, // PID unknowable at stage time; end early via allow_sleep
	}, nil
}

// stageAllowSleep stages ending every keep-awake session this user is running.
// It probes the process table for processes named `caffeinate` owned by the
// current user and builds a single `kill -TERM <pids...>` forward command. When
// none are running it returns a friendly error rather than an empty kill, so the
// caller learns the Mac is already free to sleep. There is no inverse: a
// terminated caffeinate cannot be restored (reissue keep_awake instead).
func stageAllowSleep(ctx context.Context, _ registry.Capability, _ map[string]any) (*StagedPlan, error) {
	rows, err := fetchProcesses(ctx)
	if err != nil {
		return nil, err
	}
	username := currentUsername()
	pids := caffeinatePIDs(rows, username)
	if len(pids) == 0 {
		return nil, fmt.Errorf("allow_sleep: no active keep-awake (caffeinate) session was found — this Mac is already free to sleep normally")
	}

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"End %d keep-awake session(s) (caffeinate PID(s) %s) so this Mac can sleep normally again. "+
				"This sends SIGTERM (a graceful stop) and cannot be undone; start a new session with keep_awake if needed.",
			len(pids), joinInts(pids)),
		Forward: killCaffeinateCommand(pids),
		Inverse: nil, // a stopped keep-awake cannot be restored, only reissued
	}, nil
}

// killCaffeinateCommand builds the `kill -TERM <pids...>` forward command for
// allow_sleep. The signal is hardcoded TERM (never SIGKILL) so a caffeinate
// helper always exits gracefully, and every operand is a validated integer PID
// drawn from the system's own process table — so this command can never be
// coerced into a force-kill or aimed at an arbitrary process. Kept as a small
// pure function so a test can pin that shape.
func killCaffeinateCommand(pids []int) Command {
	return Command{Binary: "kill", Args: append([]string{"-TERM"}, pidsToArgs(pids)...)}
}

// caffeinatePIDs selects, from a process snapshot, the PIDs of processes named
// exactly `caffeinate` and owned by username (with PID > 1 as a belt-and-braces
// guard so the protected low PIDs can never be targeted). This is the pure
// filter that guarantees allow_sleep can only ever signal keep-awake helpers and
// never an unrelated process; it is unit-tested against a hostile snapshot.
func caffeinatePIDs(rows []procRow, username string) []int {
	var pids []int
	for _, p := range rows {
		if p.pid <= 1 {
			continue
		}
		if username != "" && p.user != username {
			continue
		}
		if filepath.Base(p.command) != "caffeinate" {
			continue
		}
		pids = append(pids, p.pid)
	}
	return pids
}

// currentUsername returns the current user's short login name, or "" when it
// cannot be determined. On "" caffeinatePIDs falls back to matching by process
// name alone (still constrained to caffeinate), which is a safe superset for a
// single-user Mac and never widens the target beyond keep-awake helpers.
func currentUsername() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// pidsToArgs renders PIDs as decimal argv tokens for `kill`.
func pidsToArgs(pids []int) []string {
	args := make([]string, len(pids))
	for i, p := range pids {
		args[i] = strconv.Itoa(p)
	}
	return args
}

// joinInts renders PIDs as a comma-separated list for human-readable previews.
func joinInts(pids []int) string {
	parts := pidsToArgs(pids)
	out := ""
	for i, s := range parts {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// humanizeSeconds renders a whole-second duration as a friendly phrase for the
// preview (e.g. 3600 -> "1 hour", 5400 -> "1 hour 30 minutes", 90 -> "1 minute
// 30 seconds"). Only the non-zero components appear so the text stays terse.
func humanizeSeconds(total int) string {
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	var parts []string
	if h > 0 {
		parts = append(parts, plural(h, "hour"))
	}
	if m > 0 {
		parts = append(parts, plural(m, "minute"))
	}
	if s > 0 {
		parts = append(parts, plural(s, "second"))
	}
	if len(parts) == 0 {
		return "0 seconds"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

// plural renders "n unit" with a trailing "s" when n is not 1.
func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
