// executor.go is the low-level subprocess layer of the engine: it runs a fully
// resolved, fully tokenized command and renders its output into a single text
// block safe to return to an LLM.
//
// Every invocation here obeys the project's core execution axioms (see
// CLAUDE.md and .claude/rules/darwin-execution.md): commands are run via
// exec.CommandContext with an explicit positional argument vector (never a
// shell), output is capped to a token-safe budget, and the child process is
// bound to the request context so client cancellation propagates.
package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// maxOutputBytes caps text returned to the MCP client so a verbose utility
// cannot saturate the model's context window. Output beyond this is truncated
// to a head/tail window with an explicit notice (see compactOutput).
//
// The budget is deliberately generous relative to a single command's typical
// output (32 KB ≈ a few hundred lines) yet still a small fraction of a modern
// model's context window. An earlier 8 KB cap was tight enough that ordinary
// listings (e.g. a manifest dump, a directory walk) lost their middle to
// truncation, which both hid information and pushed callers toward retrying with
// narrower queries; 32 KB keeps everyday output intact while still guarding
// against a runaway multi-megabyte dump.
const maxOutputBytes = 32000

// defaultCommandTimeout bounds how long any single subprocess may run. Without
// it, a request bound only to the client's context could keep a long-running
// tool (e.g. find/du/grep rooted at "/", or a cp -R of an enormous tree) busy
// indefinitely — a time/CPU denial of service — until the client happened to
// cancel. The ceiling is deliberately generous: ordinary reads finish in well
// under a second, so two minutes leaves ample room for a legitimately large scan
// while still capping a runaway. It is a var (not a const) so tests can lower it.
var commandTimeout = 2 * time.Minute

// runResult captures the outcome of a subprocess invocation in a form suitable
// for surfacing back through MCP.
type runResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// runCommand executes binary with the given pre-tokenized arguments and no
// stdin. The caller MUST have already validated binary through the policy
// layer. Every standalone read-only capability is an argument-driven
// inspector with no piped input, so this is the path Run uses.
//
// A non-zero exit status is returned as data (in runResult.ExitCode), not as a
// Go error — for example grep exits 1 on "no match", which is a legitimate
// result the model should see rather than a transport failure.
func runCommand(ctx context.Context, binary string, args ...string) (*runResult, error) {
	return execCommand(ctx, binary, nil, args...)
}

// runCommandWithStdin is runCommand's pipeline counterpart: it wires stdin
// (a prior pipeline stage's captured output) into the child's standard input
// when non-nil. Used by RunPipeline (pipeline.go); the mutation seam reaches
// execCommand directly so a staged Command can carry its own stdin payload
// (see Command.Stdin in mutate.go).
func runCommandWithStdin(ctx context.Context, binary string, stdin []byte, args ...string) (*runResult, error) {
	return execCommand(ctx, binary, stdin, args...)
}

// execCommand is the shared implementation behind runCommand and
// runCommandWithStdin. The command is bound to a context derived from ctx with
// an independent wall-clock timeout, so the child is terminated both on client
// cancellation AND if it overruns commandTimeout. The environment is inherited
// so locale and timezone behave like the user's shell.
func execCommand(ctx context.Context, binary string, stdin []byte, args ...string) (*runResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	err := cmd.Run()
	res := &runResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		// A deadline hit is checked BEFORE the ExitError branch: the timeout kills
		// the child, which surfaces as an ExitError (signal: killed) that would
		// otherwise be misreported as an ordinary non-zero exit. Distinguish it so
		// the caller gets a clear "timed out" signal rather than a confusing -1.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, fmt.Errorf("failed to execute %s: timed out after %s", binary, commandTimeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("failed to execute %s: %w", binary, err)
	}
	return res, nil
}

// execDetached starts binary in the background and returns immediately with its
// PID, WITHOUT waiting for it to finish. It is the execution path for a
// long-lived helper (today: caffeinate) that must OUTLIVE the tool call that
// launched it.
//
// Two deliberate departures from execCommand make that possible:
//
//   - It uses exec.Command, NOT exec.CommandContext. Binding the child to the
//     request context would kill it the instant the call returns — the opposite
//     of what a keep-awake session needs. The child's own lifetime is bounded by
//     its arguments instead (caffeinate's `-t <seconds>`), and it can be ended
//     early by the paired canceller operation (allow_sleep).
//   - It places the child in its own process group (Setpgid) and releases the OS
//     process handle rather than reaping it. That detaches it from the server's
//     signal group, so a Ctrl-C delivered to the server (e.g. in a foreground
//     shell) does not also stop the background session.
//
// ctx is still honoured up front so a cancelled request starts nothing. Detached
// commands carry no stdin and produce no meaningful stdout, so neither is wired.
func execDetached(ctx context.Context, binary string, args ...string) (*runResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", binary, err)
	}
	pid := cmd.Process.Pid
	// Reap the child asynchronously. We deliberately do NOT block on it (the
	// whole point is to return immediately), but we also must not simply drop the
	// handle: on Unix a child the parent never Wait()s becomes a ZOMBIE when it
	// exits, and in a long-running server those would accumulate one per session.
	// A goroutine that only Wait()s costs nothing until the child exits, then
	// reaps it — giving us fire-and-return without leaking process-table slots.
	// (Stdout/Stderr are nil here, so they default to /dev/null: there is no pipe
	// to drain, so this Wait cannot deadlock.)
	go func() { _ = cmd.Wait() }()
	return &runResult{Stdout: fmt.Sprintf("Started %s in the background (PID %d).", binary, pid)}, nil
}

// compactOutput enforces the head/tail truncation rule: short output is returned
// unchanged; long output keeps the first and last halves of the budget with a
// notice in between describing how many bytes were dropped.
func compactOutput(raw string) string {
	if len(raw) <= maxOutputBytes {
		return raw
	}
	const window = maxOutputBytes / 2
	head := raw[:window]
	tail := raw[len(raw)-window:]
	dropped := len(raw) - 2*window
	return fmt.Sprintf(
		"%s\n\n... [%d bytes truncated to keep response within %d byte budget] ...\n\n%s",
		head, dropped, maxOutputBytes, tail,
	)
}

// formatRunResult renders a runResult into one human-readable block for an
// mcp.TextContent payload. Stderr is always preserved and flagged because macOS
// permission errors surface there; a non-zero exit code is annotated explicitly.
func formatRunResult(res *runResult) string {
	var b strings.Builder
	if res.Stdout != "" {
		b.WriteString(compactOutput(res.Stdout))
	}
	if res.Stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[stderr]\n")
		b.WriteString(compactOutput(res.Stderr))
	}
	if res.ExitCode != 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[exit code: %d]", res.ExitCode)
	}
	if b.Len() == 0 {
		return "(no output)"
	}
	return b.String()
}

// expandUserPath expands a leading ~ or ~/... segment to the current user's home
// directory and returns all other paths unchanged. We deliberately do NOT invoke
// any shell, so this tilde is the only metacharacter we honour — every other
// character is passed through as literal data.
func expandUserPath(p string) (string, error) {
	if p == "" {
		return p, nil
	}
	if p == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}
