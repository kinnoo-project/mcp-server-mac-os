// mutate_filesystem.go holds the filesystem domain's mutator(s) — the mutating
// counterpart to builders_filesystem.go's named read-only argv builders.
package engine

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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
//   - a leading "-" in the path is rejected. The argv places a "--" terminator
//     before the path, so mkdir/rmdir already treat a dash-leading value as data;
//     rejecting it anyway is a deliberate guardrail that steers the caller to
//     disambiguate with "./" (mirroring the find builder), not a "--" limitation;
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

// stageMove stages a reversible file/directory move.
//
// Forward is `mv -- <source> <finalDest>` and Inverse is `mv -- <finalDest>
// <source>`, so undo simply puts the item back where it started. Both paths are
// resolved to absolute form at stage time so the inverse is stable regardless of
// the server's working directory when undo eventually runs.
//
// Guardrails (mirroring stageMkdir's conservative stance):
//   - source and destination must be non-empty and must not begin with "-".
//     The argv always carries a "--" terminator before these operands, so mv
//     would already treat a dash-leading value as data; rejecting it is a
//     deliberate project guardrail (consistent across the mutators) that returns
//     a clear "prefix with ./" error rather than acting on a surprising "-x"
//     filename — not a workaround for any "--" limitation;
//   - source must exist;
//   - the COMPUTED destination must not already exist, which prevents silently
//     clobbering a file and guarantees the inverse can restore the original
//     layout exactly (we never overwrite something we cannot bring back).
func stageMove(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	src, dst, err := resolveSourceAndDest("move", in)
	if err != nil {
		return nil, err
	}
	finalDest, err := resolveFinalDestination("move", src, dst)
	if err != nil {
		return nil, err
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Move %s to %s. Undo will move it back to %s.", src, finalDest, src),
		Forward: Command{Binary: "mv", Args: []string{"--", src, finalDest}},
		Inverse: &Command{Binary: "mv", Args: []string{"--", finalDest, src}},
	}, nil
}

// stageCopy stages a reversible file/directory copy.
//
// Forward is `cp -R -- <source> <finalDest>` (-R so directory trees copy
// faithfully; it is harmless for a single file). The inverse must remove the
// copy we just created. Per the project recycling rule (transactional-state.md
// §3) we never purge directly: the inverse moves the freshly-made copy into the
// user's Trash, so undo is recoverable rather than destructive. Because staging
// already proved finalDest did not exist beforehand, the inverse only ever
// trashes the copy this operation produced — never pre-existing user data.
func stageCopy(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	src, dst, err := resolveSourceAndDest("copy", in)
	if err != nil {
		return nil, err
	}
	finalDest, err := resolveFinalDestination("copy", src, dst)
	if err != nil {
		return nil, err
	}
	// Refuse a copy that would not comfortably fit on the destination volume,
	// so a recursive copy of a huge tree cannot fill the disk (a storage DoS).
	if err := ensureCopyFits(ctx, src, finalDest); err != nil {
		return nil, err
	}
	trashDest, err := trashPathFor(finalDest)
	if err != nil {
		return nil, fmt.Errorf("copy: %w", err)
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Copy %s to %s. Undo will move the copy to the Trash (%s).", src, finalDest, trashDest),
		Forward: Command{Binary: "cp", Args: []string{"-R", "--", src, finalDest}},
		Inverse: &Command{Binary: "mv", Args: []string{"--", finalDest, trashDest}},
	}, nil
}

// stageRemove stages a reversible "delete" by recycling to the Trash.
//
// This honours the recycling rule (transactional-state.md §3): instead of an
// irreversible `rm`, the forward command MOVES the target into the user's
// ~/.Trash with a collision-free name, and the inverse moves it straight back.
// The user therefore has two independent restore paths — the engine's undo
// token and Finder's own "Put Back" — and no data is ever truly purged by this
// tool.
//
// Guardrails: path must be non-empty, must not begin with "-", must exist, and
// must resolve to absolute form before the move so the inverse is cwd-stable.
func stageRemove(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	path, _ := getString(in, "path")
	abs, err := validateExistingOperand("remove", "path", path)
	if err != nil {
		return nil, err
	}
	trashDest, err := trashPathFor(abs)
	if err != nil {
		return nil, fmt.Errorf("remove: %w", err)
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Move %s to the Trash (%s). Undo will restore it to %s.", abs, trashDest, abs),
		Forward: Command{Binary: "mv", Args: []string{"--", abs, trashDest}},
		Inverse: &Command{Binary: "mv", Args: []string{"--", trashDest, abs}},
	}, nil
}

// resolveSourceAndDest validates the shared "source"/"destination" parameter
// pair used by move and copy: it enforces presence and the dash-leading
// guardrail on both, confirms the source exists, and returns both paths in
// absolute form. The destination is NOT required to exist (it is the thing being
// created); only its leading-dash safety is checked here.
func resolveSourceAndDest(op string, in map[string]any) (src, dst string, err error) {
	rawSrc, _ := getString(in, "source")
	src, err = validateExistingOperand(op, "source", rawSrc)
	if err != nil {
		return "", "", err
	}
	rawDst, _ := getString(in, "destination")
	if rawDst == "" {
		return "", "", fmt.Errorf("%s: 'destination' is required", op)
	}
	if strings.HasPrefix(rawDst, "-") {
		return "", "", fmt.Errorf("%s: destination %q begins with '-' and is not allowed; prefix it with ./", op, rawDst)
	}
	dst, err = filepath.Abs(rawDst)
	if err != nil {
		return "", "", fmt.Errorf("%s: resolving destination %q: %w", op, rawDst, err)
	}
	return src, dst, nil
}

// resolveFinalDestination computes the concrete path a move/copy will create and
// proves it does not already exist.
//
// When dst is an existing directory the source is placed INSIDE it under its own
// basename — matching the native mv/cp behaviour and the user's mental model of
// "move X into folder Y" (so "move test.txt to ~/Desktop" lands at
// ~/Desktop/test.txt). Refusing a pre-existing final destination is what keeps
// the operation non-clobbering and lets the inverse restore the prior state
// exactly.
//
// The final destination's PARENT directory must exist and be a directory. mv/cp
// do not create intermediate directories, so without this check a plan could
// stage cleanly yet be guaranteed to fail when its forward command runs — which
// would violate the "a staged plan is executable exactly as built" contract the
// rest of the engine relies on (cf. trashPathFor's ~/.Trash fail-fast).
func resolveFinalDestination(op, src, dst string) (string, error) {
	finalDest := dst
	if info, err := os.Stat(dst); err == nil && info.IsDir() {
		finalDest = filepath.Join(dst, filepath.Base(src))
	}
	if _, err := os.Stat(finalDest); err == nil {
		return "", fmt.Errorf("%s: destination %q already exists; refusing to overwrite (undo could not restore the original)", op, finalDest)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("%s: cannot inspect destination %q: %w", op, finalDest, err)
	}
	parent := filepath.Dir(finalDest)
	if info, err := os.Stat(parent); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s: destination parent directory %q does not exist (mv/cp will not create it)", op, parent)
		}
		return "", fmt.Errorf("%s: cannot inspect destination parent %q: %w", op, parent, err)
	} else if !info.IsDir() {
		return "", fmt.Errorf("%s: destination parent %q is not a directory", op, parent)
	}
	return finalDest, nil
}

// validateExistingOperand applies the standard operand guardrails to a single
// user-supplied path that must already exist on disk: it rejects an empty value
// and a leading dash. Every argv built from these operands already places a "--"
// terminator before them, so the dash rejection is not needed for safety — it is
// a conservative project guardrail (consistent across the mutators) that turns a
// confusing "-x" filename into a clear "prefix with ./" error. It then returns
// the path in absolute form so any inverse command built from it is stable
// regardless of the working directory at undo time.
func validateExistingOperand(op, field, raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%s: '%s' is required", op, field)
	}
	if strings.HasPrefix(raw, "-") {
		return "", fmt.Errorf("%s: %s %q begins with '-' and is not allowed; prefix it with ./", op, field, raw)
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("%s: resolving %s %q: %w", op, field, raw, err)
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s: %s %q does not exist", op, field, abs)
		}
		return "", fmt.Errorf("%s: cannot inspect %s %q: %w", op, field, abs, err)
	}
	return abs, nil
}

// trashPathFor returns a collision-free destination inside the current user's
// ~/.Trash for the given source path. The macOS Trash is the project's mandated
// recycling bin for deletions and undo-deletions (transactional-state.md §3):
// routing items here gives the user an immediate manual restore path and makes
// the operations reversible. When a same-named item already sits in the Trash,
// a numeric suffix is appended (mirroring Finder's own disambiguation) so an
// existing trashed item is never overwritten.
//
// The Trash directory's existence is verified here, at stage time, rather than
// being discovered as a failure when the staged `mv` later runs: a StagedPlan is
// meant to be executable exactly as built, so a missing or non-directory
// ~/.Trash must fail staging with a clear message instead of producing a plan
// that is doomed to fail at execute/undo time.
func trashPathFor(src string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory for Trash: %w", err)
	}
	trashDir := filepath.Join(home, ".Trash")
	info, err := os.Stat(trashDir)
	if err != nil {
		return "", fmt.Errorf("Trash directory %s is not available: %w", trashDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("Trash path %s exists but is not a directory", trashDir)
	}
	base := filepath.Base(src)
	if candidate := filepath.Join(trashDir, base); !pathExists(candidate) {
		return candidate, nil
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; i < 10000; i++ {
		candidate := filepath.Join(trashDir, fmt.Sprintf("%s %d%s", stem, i, ext))
		if !pathExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find a free name in %s for %q", trashDir, base)
}

// pathExists reports whether a path currently exists. A stat error other than
// "not found" is treated as "exists" so trashPathFor errs toward picking a fresh
// name rather than risking an overwrite of something it could not inspect.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil || !os.IsNotExist(err)
}

// copyHeadroomBytes is the amount of free space a copy must leave behind on the
// destination volume. Bringing a boot volume to (near) zero free destabilizes
// macOS — apps can't save, logs stop — so we keep a comfortable reserve rather
// than allowing a copy right up to the last byte.
const copyHeadroomBytes = 256 << 20 // 256 MiB

// ensureCopyFits refuses a copy whose source is too large to fit on the
// destination volume while preserving copyHeadroomBytes of free space — the
// guardrail against a recursive copy filling the disk.
//
// It fails OPEN: if the source size or the volume's free space cannot be
// measured, the copy is allowed. A safety estimate must never become the reason
// a legitimate copy is blocked; the worst case of failing open is that we fall
// back to the pre-guard behavior, while the common case is caught.
func ensureCopyFits(ctx context.Context, src, finalDest string) error {
	size, err := treeSizeBytes(ctx, src)
	if err != nil {
		// A cancelled request should surface; an unmeasurable tree should not block.
		if ctx.Err() != nil {
			return err
		}
		return nil
	}
	avail, ok := availableBytesOnVolume(filepath.Dir(finalDest))
	if !ok {
		return nil // cannot determine free space: do not block the copy
	}
	if !copyFits(size, avail) {
		return fmt.Errorf("copy: source is about %s but only %s is free on the destination volume; refusing to risk filling the disk",
			formatBytes(size), formatBytes(avail))
	}
	return nil
}

// copyFits reports whether a source of sizeBytes can be copied onto a volume with
// availBytes free while keeping copyHeadroomBytes in reserve.
func copyFits(sizeBytes, availBytes int64) bool {
	return sizeBytes+copyHeadroomBytes <= availBytes
}

// treeSizeBytes sums the apparent sizes of the regular files under root,
// honoring ctx so a cancelled request stops the walk. Directories, symlinks, and
// special files contribute nothing. Apparent size can exceed real disk usage for
// sparse files or hard links — a deliberately conservative bias for a
// "will it fit?" check (it errs toward refusing a borderline copy).
func treeSizeBytes(ctx context.Context, root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// availableBytesOnVolume returns the bytes available to an unprivileged user on
// the volume containing path, and whether the figure could be determined.
func availableBytesOnVolume(path string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}
