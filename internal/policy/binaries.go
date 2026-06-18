// Package policy is the trust boundary for which executables the server may run.
//
// # Architecture role
//
// Every capability names a bare binary (e.g. "ls"). Before the engine executes
// anything, it asks this package to resolve that name to a concrete absolute
// path AND to prove the path lives under a trusted Darwin system directory.
// Centralising this in one package means the "what is allowed to run" decision
// exists in exactly one auditable place, rather than being re-derived at each
// call site.
//
// This guards against rogue binary substitution: a "grep" or "ls" planted
// earlier on $PATH (or in the working directory) must never be invoked in place
// of the genuine system utility.
package policy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// allowedBinDirs are the only parent directories a resolved binary may live in.
// These are the canonical locations of Darwin's first-party command-line tools.
var allowedBinDirs = []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin"}

// ResolveBinary looks up a bare utility name and returns its absolute path only
// if that path resides under one of the trusted system directories.
//
// Resolution order:
//  1. Reject names containing path separators outright — callers must pass a
//     bare name like "ls", never a path, so a caller can never point us at an
//     arbitrary executable.
//  2. Try exec.LookPath (honours $PATH).
//  3. Fall back to scanning the trusted directories directly, which keeps the
//     server working even when $PATH is empty — common when the process is
//     spawned as a child by an MCP client.
//
// The final path is always re-checked against allowedBinDirs, so even a
// LookPath hit outside the trusted set is rejected.
func ResolveBinary(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("policy: binary name %q must not contain path separators", name)
	}

	abs, err := exec.LookPath(name)
	if err != nil {
		// PATH lookup failed; scan the trusted directories directly.
		for _, d := range allowedBinDirs {
			candidate := filepath.Join(d, name)
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				abs = candidate
				err = nil
				break
			}
		}
		if err != nil {
			return "", fmt.Errorf("policy: could not locate %q in trusted system directories: %w", name, err)
		}
	}

	abs, err = filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	if !underTrustedDir(abs, name) {
		return "", fmt.Errorf("policy: resolved binary %q is outside trusted Darwin directories %v", abs, allowedBinDirs)
	}
	return abs, nil
}

// underTrustedDir reports whether abs sits directly within one of the trusted
// directories. The trailing-separator comparison prevents a prefix like
// "/usr/bins-evil" from matching "/usr/bin".
func underTrustedDir(abs, name string) bool {
	for _, d := range allowedBinDirs {
		if strings.HasPrefix(abs+string(os.PathSeparator), d+string(os.PathSeparator)) ||
			abs == filepath.Join(d, name) {
			return true
		}
	}
	return false
}
