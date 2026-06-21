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

// ResolveBinary looks up a bare utility name and returns the absolute path of
// the genuine system tool under one of the trusted Darwin directories.
//
// Resolution order:
//  1. Reject names containing path separators outright — callers must pass a
//     bare name like "ls", never a path, so a caller can never point us at an
//     arbitrary executable.
//  2. Try exec.LookPath (honours $PATH), but ACCEPT its result only when it
//     already lives under a trusted directory. A PATH hit outside the trusted
//     set — a rogue "grep" planted in the working directory, or a foreign
//     "sqlite3" from an SDK earlier on $PATH — is deliberately IGNORED rather
//     than treated as an error, so we then...
//  3. ...fall back to scanning the trusted directories directly for the real
//     system binary. This both blocks rogue substitution AND keeps the server
//     working when $PATH is empty or unusual (common when the process is spawned
//     as a child by an MCP client, or on a CI runner whose PATH front-loads a
//     non-system toolchain).
//
// The untrusted PATH hit is never executed in any case; the only question is
// whether we can still find the legitimate tool, which step 3 answers.
func ResolveBinary(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("policy: binary name %q must not contain path separators", name)
	}

	// Honour a PATH hit only if it is itself trusted; otherwise ignore it and
	// fall through to the trusted-directory scan below.
	if p, err := exec.LookPath(name); err == nil {
		if abs, aerr := filepath.Abs(p); aerr == nil && underTrustedDir(abs, name) {
			return abs, nil
		}
	}

	// Scan the trusted directories directly for the genuine system binary.
	for _, d := range allowedBinDirs {
		candidate := filepath.Join(d, name) // d is absolute, so candidate is too
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("policy: could not locate %q in trusted system directories %v", name, allowedBinDirs)
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
