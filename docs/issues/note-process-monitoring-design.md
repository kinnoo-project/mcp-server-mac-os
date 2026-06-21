**note**
Process & resource monitoring (`process` domain) — design decisions and known
limitations.

New read-only builtins over stable, unprivileged system utilities:
- `list_processes` — one `ps -Axo pid,ppid,user,pcpu,pmem,etime,state,comm`,
  then sort/filter/limit in Go so the params compose off a single snapshot. The
  `filter` is matched in-process and never reaches a subprocess; `comm` is the
  full executable path, which both classifies origin and names the responsible
  binary.
- `process_info` — several small `ps -o …=` reads (header-suppressed) plus a
  `launchctl list` cross-reference for the "auto-started by launchd?" line.
- `cpu_load` — `sysctl -n vm.loadavg hw.ncpu`; reports per-core load (load ÷
  cores), the figure that actually indicates overload.
- `memory_stats` — `sysctl -n hw.memsize` + plain `vm_stat`. NOTE: the sampling
  form `vm_stat <interval>` prints a different, columnar layout, so we
  deliberately call plain `vm_stat`, whose "Pages free: N." report is what the
  parser scales by the reported page size.
- `gpu_stats` — `ioreg -r -d 1 -c IOAccelerator`, parsing the
  `PerformanceStatistics` dict.
- `startup_items` — `launchctl list`, defaulting to non-Apple labels.

Mutations (both staged → execute, both `Inverse == nil`):
- `quit_process` — resolves a PID to its `.app` bundle and sends the GUI app the
  normal Quit Apple Event (reusing `quitScript`). A non-app PID is refused.
- `terminate_process` — `kill -TERM <pid>`, signal hardcoded. A GUI-app PID is
  refused (directed to `quit_process`), so the two mutators cleanly partition
  the process space: GUI apps quit gracefully, everything else gets SIGTERM.

**issue**
Per-process GPU usage is NOT available. macOS only exposes per-process GPU
counters through `powermetrics`, which refuses to run as a non-superuser, and
this server never escalates privileges or uses sudo. `gpu_stats` therefore
reports whole-device utilization only (via the unprivileged IOKit
`IOAccelerator` performance counters). The documented proxy for "which app is
driving the GPU" is to spot GPU-helper processes (e.g. `WindowServer`,
`*.WebKit.GPU`) high in `list_processes` by CPU.

**fixed**
Won't fix without an admin-privilege model. Whole-device GPU stats plus the
helper-process proxy are the unprivileged ceiling; documented in the manifest
summary and the `gpu_stats` output itself.

**issue**
Classic **Login Items** are not listed by `startup_items`. Only launchd-managed
agents/daemons (`launchctl list`) are covered. Reading the classic Login Items
list requires a `System Events` AppleScript query, which trips an Automation
permission prompt — deferred to keep this capability set permission-free.

**fixed**
Deferred. `startup_items` notes the exclusion in its output; a future revision
could add an Automation-gated login-items reader alongside the launchd inventory.

**issue**
Force-kill (SIGKILL) is intentionally **not** offered. Both stop paths are
graceful (Quit Apple Event / SIGTERM), staged for human approval, and
non-undoable. SIGKILL gives a process no chance to save, flush, or clean up, so
there is deliberately no parameter or code path that can send it.

**fixed**
By design — `terminate_process` hardcodes `-TERM` and exposes no signal
parameter; `quit_process` only ever sends the Quit event.
