**note**
Design choices for the system machine-info reads (U2 of the capability
roadmap): `about_this_mac`, `uptime`, `disk_usage`, `software_update_check`,
plus the battery-health extension to `power_status`.

- **All fixed argv, zero params.** None of these takes a model-controlled
  value, so there is no injection surface and no `reviewedFreeTextBuiltins`
  entry to add. `uptime` is the manifest-only generic-builder case; the rest
  are builtins so their output can be parsed/curated in-process.
- **`disk_usage` uses `df -H`, not `diskutil`.** `diskutil` is on the
  security deny-list (it can erase/repartition disks) and staying off it
  matters more than its richer volume metadata. `df -H` covers the everyday
  questions (free space, what's mounted) in Finder-style SI units; rows are
  filtered to device-backed (`/dev/…`) filesystems, and a mount point
  containing spaces is recovered by byte offset (ninth column through end of
  line), not by joining fields.
- **`software_update_check` is read-only by construction** — `softwareupdate
  -l` lists but never installs (installing needs admin rights → the
  `open_settings` hand-off). Its verdict lands on *stderr* behind a progress
  banner, with found updates on *stdout*, so the renderer merges both streams
  and drops the banner. Runtime is 10–60s against Apple's servers; the
  engine's 2-minute wall clock bounds a slow scan with a clear timeout error.
  No per-capability timeout mechanism was added for one op.
- **Battery health folds into `power_status`** rather than becoming a new op:
  same question ("how's my battery?"), same domain, and `system_profiler
  SPPowerDataType -json` parses cleanly. Best-effort: desktops without a
  battery (and profiler hiccups) simply omit the line.
- Serial number appears in `about_this_mac` output — machine-identifying but
  the user's own, and only ever returned to their client; committed tests use
  a fake (`TESTSERIAL123`).
