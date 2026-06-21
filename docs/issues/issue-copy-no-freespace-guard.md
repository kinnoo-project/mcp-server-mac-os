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
