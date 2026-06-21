// mutate_process.go implements the two mutating process operations, both of
// which STOP a process gracefully and neither of which can be undone:
//
//   - quit_process sends a GUI application the normal Quit command (the same
//     Apple Event as choosing Quit from its menu) so it can prompt to save and
//     shut down cleanly. It only applies to apps; a daemon/CLI PID is refused
//     with a pointer to terminate_process.
//   - terminate_process sends a background/daemon process SIGTERM, the polite
//     "please exit" signal a well-behaved process can trap to clean up first.
//
// # Why no force-kill, ever
//
// Neither path can send SIGKILL (force kill). SIGKILL is delivered by the kernel
// and cannot be caught by the target, so the process gets no chance to save,
// flush buffers, remove temp files, or stop its children — the classic cause of
// data loss and corrupted state. quit_process uses the Quit Apple Event;
// terminate_process hardcodes the TERM signal in argv and exposes NO signal
// parameter, so there is structurally no way for a caller to escalate to
// SIGKILL through this server.
//
// # Why the inputs are safe
//
// The only model input is an integer PID, validated to be > 1 (so it can never
// be read as a command-line flag, and the kernel/launchd low PIDs are never
// targeted). quit_process derives the app name from the running process's own
// executable path, then passes it as DATA after the osascript "--" terminator
// (osascriptCommand) and additionally runs it through validateAppNameValue — so
// even a maliciously named .app bundle cannot inject an osascript option.
package engine

import (
	"context"
	"fmt"
	"strconv"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// stageQuitProcess stages a graceful quit of the GUI application that owns a
// PID. Staging resolves the PID to its executable path and the .app bundle it
// belongs to; a PID that is not a GUI app is refused (a daemon cannot be sent a
// Quit Apple Event — terminate_process is the right tool for those). The forward
// command is the same AppleScript quit used by application.quit_application, and
// there is no inverse: relaunching is not a true reversal and unsaved work may
// be lost, so the change routes through the human-approval gate with no undo.
func stageQuitProcess(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	pid, err := requirePID("quit_process", in)
	if err != nil {
		return nil, err
	}

	exePath, ok := processExePath(ctx, pid)
	if !ok {
		return nil, fmt.Errorf("quit_process: no process with PID %d is currently running", pid)
	}
	appName := appNameFromExePath(exePath)
	if appName == "" {
		return nil, fmt.Errorf("quit_process: PID %d (%s) is not a GUI application, so it has no Quit command; use terminate_process to send it SIGTERM instead", pid, exePath)
	}
	// Defence in depth on top of the osascript "--" terminator: reject a bundle
	// name that is empty, control-laden, or dash-leading before it becomes argv.
	appName, err = validateAppNameValue("quit_process", "application", appName)
	if err != nil {
		return nil, err
	}

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Gracefully quit %q (PID %d) by sending it the normal Quit command, so it can prompt to save and shut down cleanly. Unsaved work may still be lost, and this cannot be undone.",
			appName, pid),
		Forward: osascriptCommand(quitScript, appName),
		Inverse: nil,
	}, nil
}

// stageTerminateProcess stages sending SIGTERM to a process by PID. Staging
// confirms the process exists (PIDs are recycled, so we name the current
// occupant in the preview to avoid signalling the wrong one) and builds a
// `kill -TERM <pid>` command with the signal hardcoded. There is no inverse: a
// terminated process cannot be restored.
func stageTerminateProcess(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	pid, err := requirePID("terminate_process", in)
	if err != nil {
		return nil, err
	}

	exePath, ok := processExePath(ctx, pid)
	if !ok {
		return nil, fmt.Errorf("terminate_process: no process with PID %d is currently running", pid)
	}

	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Send SIGTERM (the polite 'please exit' signal) to PID %d (%s). A well-behaved process will clean up and shut down; this is NOT a force-kill, but it cannot be undone. If the process ignores SIGTERM it will keep running.",
			pid, exePath),
		// Signal is hardcoded TERM and the pid is a validated integer, so this can
		// never be coerced into a force-kill (SIGKILL) or read as a flag.
		Forward: Command{Binary: "kill", Args: []string{"-TERM", strconv.Itoa(pid)}},
		Inverse: nil,
	}, nil
}

// requirePID extracts and validates the shared "pid" parameter for the mutating
// process operations: it must be present and greater than 1. Rejecting <= 1
// keeps the kernel (0) and launchd (1) off-limits and guarantees the value can
// never be read as a command-line option.
func requirePID(op string, in map[string]any) (int, error) {
	pid, ok := getInt(in, "pid")
	if !ok {
		return 0, fmt.Errorf("%s: 'pid' is required", op)
	}
	if pid <= 1 {
		return 0, fmt.Errorf("%s: 'pid' must be greater than 1 (got %d); PID 1 and below are protected", op, pid)
	}
	return pid, nil
}

// processExePath returns the executable path of a running PID via `ps -o comm=`,
// and false when no such process exists. It is the stage-time existence check
// shared by both mutators.
func processExePath(ctx context.Context, pid int) (string, bool) {
	bin, err := policy.ResolveBinary("ps")
	if err != nil {
		return "", false
	}
	res, err := runCommand(ctx, bin, "-o", "comm=", "-p", strconv.Itoa(pid))
	if err != nil {
		return "", false
	}
	path := firstLine(res.Stdout)
	if path == "" {
		return "", false
	}
	return path, true
}
