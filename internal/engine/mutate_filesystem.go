// mutate_filesystem.go holds the filesystem domain's mutator(s) — the mutating
// counterpart to builders_filesystem.go's named read-only argv builders.
package engine

import (
	"context"
	"fmt"
	"os"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// stageMkdir stages a reversible directory creation.
//
// Forward is `mkdir -- <path>` and Inverse is `rmdir -- <path>`. The inverse is
// deliberately rmdir rather than a recursive remove: rmdir refuses a non-empty
// directory, so undo can only ever remove the empty directory this operation
// created — it can never destroy files the user added afterwards.
//
// Two guardrails run before a plan is produced:
//   - a leading "-" in the path is rejected (mkdir/rmdir would parse it as a
//     flag despite the "--" terminator's protection of later operands), steering
//     the caller to disambiguate with "./", mirroring the find builder;
//   - the target must not already exist, which keeps the create meaningful and
//     guarantees the rmdir inverse is safe (we never adopt — and then delete — a
//     directory we did not create).
func stageMkdir(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	path, _ := getString(in, "path")
	if path == "" {
		return nil, fmt.Errorf("mkdir: 'path' is required")
	}
	if strings.HasPrefix(path, "-") {
		return nil, fmt.Errorf("mkdir: path %q begins with '-' and is not allowed; prefix it with ./", path)
	}
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("mkdir: %q already exists; refusing to create (undo would otherwise delete a directory this action did not create)", path)
	} else if !os.IsNotExist(err) {
		// A stat error other than "not found" (e.g. a permission problem on the
		// parent) means we cannot safely reason about the target; surface it.
		return nil, fmt.Errorf("mkdir: cannot inspect %q: %w", path, err)
	}

	return &StagedPlan{
		Preview: fmt.Sprintf("Create directory %s. Undo will remove it (only if it is still empty).", path),
		Forward: Command{Binary: "mkdir", Args: []string{"--", path}},
		Inverse: &Command{Binary: "rmdir", Args: []string{"--", path}},
	}, nil
}
