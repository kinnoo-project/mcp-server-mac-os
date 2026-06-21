**issue**
The `copy` mutator (`stageCopy` in `internal/engine/mutate_filesystem.go`) stages
a `cp -R` of a user-supplied source to a destination. It validates that the
destination does not already exist and that its parent directory exists, but it
does NOT check the size of the source against the free space on the destination
volume. Copying a very large directory (or a path that expands greatly, e.g. one
containing large sparse files or many hard links) can therefore fill the disk —
a storage-exhaustion denial of service. A full boot volume can in turn destabilize
the system (apps fail to save, logs stop, etc.).

This was identified while building the security test suite. Like the executor
timeout gap, it was deferred to a follow-up hardening PR rather than the
test-only security suite. The DoS-intent eval case `sec_dos_fill_disk_no_execute`
checks that the model does not auto-commit such a request, but there is no
engine-level guard yet.

Suggested fix: at stage time, estimate the source size (bounded walk, or `du`)
and compare it against the destination volume's available space (e.g.
`syscall.Statfs`), refusing the copy with a clear error when it would not
comfortably fit. Keep the estimate bounded so the guard itself cannot become a
slow walk over a huge tree.

**fixed**
`stageCopy` now calls `ensureCopyFits`, which sums the source's regular-file
sizes (`treeSizeBytes`, a ctx-honoring `filepath.WalkDir`) and compares it
against the destination volume's available space (`availableBytesOnVolume` via
`syscall.Statfs`), refusing the copy when it would not leave at least
`copyHeadroomBytes` (256 MiB) free. The guard fails OPEN — if the source size or
free space cannot be measured it allows the copy, so a measurement limitation
never blocks a legitimate operation. The size sum uses apparent file sizes, a
conservative bias (it can overestimate for sparse files / hard links, erring
toward refusing a borderline copy). The walk runs at stage time and is bounded by
the request context (and now also by the executor's wall-clock timeout once the
copy itself runs). Tests: `TestBounds_CopyFits` (the pure fit decision) and
`TestBounds_TreeSizeBytes` (the size estimate) in
`internal/engine/bounds_test.go`; the existing `TestStageCopy_PlanAndRoundTrip`
now also exercises the guard's happy path.
