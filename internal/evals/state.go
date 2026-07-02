// state.go defines filesystem post-conditions for an Expectation and the
// os.Stat pass that checks them after a turn's tool calls settle.
//
// This is the assertion layer that catches *execution-semantics* bugs — a case
// selects the right operation, but the operation leaves the system in the wrong
// state. The motivating example: "move my screenshots into ~/Desktop/screenshots"
// selected filesystem/move correctly, yet when the destination directory already
// existed the file landed at the wrong final path. A selection-only check passes
// that; a State.Exists naming the *intended* final path fails it, because
// os.Stat of that path returns not-exist.
//
// The check lives here, apart from CheckExpectation, on purpose: CheckExpectation
// is documented as pure (no I/O) so it can be unit-tested against a hand-built
// TurnOutcome with no filesystem. CheckState does touch the filesystem, so it is
// its own function — unit-tested against real temp directories instead.
package evals

import (
	"fmt"
	"os"
)

// State is a set of declarative filesystem post-conditions. Every field is
// optional; an empty State checks nothing. Paths may contain "{{scratch}}" and
// "{{unique}}" placeholders, resolved by the substituter passed to CheckState.
type State struct {
	// Exists names paths that must exist after the turn (file or directory).
	Exists []string `json:"exists,omitempty"`
	// Absent names paths that must NOT exist after the turn — e.g. the original
	// location of something that should have been moved away.
	Absent []string `json:"absent,omitempty"`
	// IsDir names paths that must exist AND be directories.
	IsDir []string `json:"is_dir,omitempty"`
}

// CheckState verifies st's post-conditions against the real filesystem, applying
// subst to every path first so "{{scratch}}"/"{{unique}}" placeholders resolve.
// It returns nil when all conditions hold, or the first violation as a
// descriptive error (naming the resolved path and what was expected) so a
// failing eval is debuggable from the message alone. A nil st checks nothing.
func CheckState(st *State, subst func(string) string) error {
	if st == nil {
		return nil
	}

	for _, raw := range st.Exists {
		path := subst(raw)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("state: expected %q to exist, but stat failed: %v", path, err)
		}
	}

	for _, raw := range st.Absent {
		path := subst(raw)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("state: expected %q to be absent, but it exists", path)
		} else if !os.IsNotExist(err) {
			// A non-"not exist" error (e.g. a permission problem) is ambiguous:
			// we cannot conclude the path is absent, so surface it rather than
			// silently treating the case as passing.
			return fmt.Errorf("state: checking that %q is absent: %v", path, err)
		}
	}

	for _, raw := range st.IsDir {
		path := subst(raw)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("state: expected directory %q, but stat failed: %v", path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("state: expected %q to be a directory, but it is not", path)
		}
	}

	return nil
}
