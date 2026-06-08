package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxOutputBytes caps text returned to the MCP client to avoid saturating the
// LLM context window. Output beyond this is truncated to a head/tail window
// with an explicit notice in the middle (see compactOutput).
const maxOutputBytes = 8000

// allowedBinDirs are the absolute parent directories permitted for any system
// utility we shell out to. This blocks rogue binary substitution from a user
// PATH (e.g., a "grep" planted earlier in $PATH).
var allowedBinDirs = []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin"}

// resolveBinary looks up a utility name and verifies its absolute path lives
// under one of the trusted Darwin system directories.
func resolveBinary(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("binary name %q must not contain path separators", name)
	}
	abs, err := exec.LookPath(name)
	if err != nil {
		// Fall back to scanning the trusted directories directly. This makes
		// the server work even when PATH is empty (as is common when the
		// process is spawned by Claude as a child).
		for _, d := range allowedBinDirs {
			candidate := filepath.Join(d, name)
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				abs = candidate
				err = nil
				break
			}
		}
		if err != nil {
			return "", fmt.Errorf("could not locate %q in trusted system directories: %w", name, err)
		}
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	for _, d := range allowedBinDirs {
		if strings.HasPrefix(abs+string(os.PathSeparator), d+string(os.PathSeparator)) || abs == filepath.Join(d, name) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("resolved binary %q is outside trusted Darwin system directories %v", abs, allowedBinDirs)
}

// expandUserPath expands a leading ~ or ~/... segment to the current user's
// home directory. All other paths are returned unchanged. We deliberately do
// NOT call any shell, so this is the only meta-character we honor.
func expandUserPath(p string) (string, error) {
	if p == "" {
		return p, nil
	}
	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
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

// runResult captures the outcome of a subprocess invocation in a form suitable
// for surfacing back through MCP.
type runResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// runCommand executes binary with the given pre-tokenized arguments. The
// command is bound to ctx so that client cancellation propagates to the child
// process. The caller MUST have already validated `binary` via resolveBinary.
//
// stdin is intentionally not wired up: every tool here is a read-only,
// argument-driven inspector with no need for piped input. Environment is
// inherited so locale/timezone behave like the user's shell.
func runCommand(ctx context.Context, binary string, args ...string) (*runResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := &runResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			// Non-zero exit is a legitimate, expected outcome (e.g. grep with
			// no matches returns 1). Surface it as data, not a transport
			// error.
			return res, nil
		}
		return res, fmt.Errorf("failed to execute %s: %w", binary, err)
	}
	return res, nil
}

// compactOutput enforces the head/tail truncation rule from
// .claude/rules/darwin-execution.md. If raw is short enough it is returned
// unchanged.
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

// formatRunResult renders a runResult into a single human-readable text block
// suitable for an mcp.TextContent payload. Stderr is always preserved (and
// flagged) because permission errors from macOS surface there.
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
