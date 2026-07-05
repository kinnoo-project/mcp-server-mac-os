**note**

V7 — new `storage` domain: Time Machine status, volume/disk listing, and disk
image attach/detach. This is the second deny-list edit of Phase 2 (V5 was the
first): `tmutil`, `diskutil`, and `hdiutil` come off `deniedBinaries` and their
safety moves to verb pinning, exactly as `csrutil`/`spctl` did in V5. The domain
adds the 21st MCP tool (`storage`) — 24 tools total with execute/undo/pipeline.

## The eight operations and why each risk tier

Five are read-only builtins, three are staged mutations:

- `time_machine_status`, `list_backups` — `tmutil` reads (status/latestbackup,
  listbackups/listlocalsnapshots). RO/none, fixed argv, no input.
- `list_volumes` — `diskutil list`. RO/none, fixed argv.
- `volume_info` — `diskutil info <id>`. RO/none; the identifier is the only input.
- `eject_volume` — the **advisory** op (see below). RO/none.
- `mount_volume` — `diskutil mount <id>`. Staged, medium, irreversible-by-server.
- `attach_disk_image` — `hdiutil attach <path.dmg>`. Staged, medium,
  irreversible-by-server.
- `detach_disk_image` — `hdiutil detach <mountpoint>`. Staged, medium,
  irreversible.

## Why the three mutations carry no auto-undo (the caffeinate precedent)

A staged inverse must be fully resolved at stage time (the engine bakes the
forward AND inverse commands when it stages, so commit/undo just replay them). But
the natural reversal of a mount or an attach needs a fact that only exists *after*
the forward runs: the mount point / device the system assigns. `hdiutil attach`
in particular only prints the assigned mount point in its own output. That is the
same tension V3 hit with `keep_awake` (whose detached `caffeinate` PID is unknown
until it starts), and it is resolved the same way: **no inverse, classified
irreversible, with an explicit paired reversal instead** of a synthesized undo.

- `mount_volume` → the result hands the user the exact `diskutil unmount <id>`
  command to run themselves (the advisory-command pattern again).
- `attach_disk_image` → reverse with the separate `detach_disk_image` operation.
- `detach_disk_image` → re-attaching needs the original image path, which detach
  does not carry, so it too is irreversible; re-open with `attach_disk_image`.

They are still **staged** (not auto-commit) because a volume appearing or
disappearing is a real state change a human should approve first.

## The advisory-command pattern (new in this phase): `eject_volume`

Physically ejecting a disk while an app has a file open on it can interrupt that
app or corrupt an in-flight write, and an automatically-fired eject aimed at the
wrong identifier is hard to take back. So `eject_volume` **never ejects**. It is a
read-only op that confirms the target exists (a read-only `diskutil info` probe)
and returns the exact `diskutil eject <id>` command for the human to run, with a
consequences warning. The tool description tells the model this up front, and the
eval `eject_routes_to_advisory` pins that an eject request routes here and forbids
`execute`. This is the reusable template for future "the server instructs, the
human executes" operations where auto-firing is too dangerous but the intent is
legitimate.

## Injection posture — no `--` terminator, so an allowlist is the defense

`diskutil` and `hdiutil` have no usable `--` end-of-options terminator, so a
dash-leading value could otherwise be read as one of their own flags. The shared
`validateVolumeIdentifier` confines every disk/volume identifier to a strict
allowlist: a BSD disk identifier (`disk0`, `disk2s1`, APFS `disk1s1s1`) or a
single-component `/Volumes/<name>` mount path with no embedded slash (so a value
can never traverse to `/Volumes/../etc`). `attach_disk_image`'s path rides
`validateExistingOperand` (dash-rejected, resolved absolute, must exist), matching
the `sips` pattern. Each op ships an `-e`-as-input regression, and every argv is a
pinned pure builder function so `TestSecurity_ConstrainedBinaryVerbs` proves the
verbs without launching anything.

## Verb-pin table extension

`security_verbs_test.go`'s `constrainedBinaryVerbs` gains three rows —
`tmutil` (status/latestbackup/listbackups/listlocalsnapshots; never delete/
restore/setdestination), `diskutil` (list/info/mount; never erase*/partitionDisk/
reformat/eject/unmount), `hdiutil` (attach/detach/imageinfo; never create/convert/
resize/burn) — with a probe row per capability. Exact-token matching keeps the
allowed `mount` distinct from the forbidden `unmount`/`mountDisk`.

## Deferred / out of scope

- **No erase, partition, reformat, repair, or rename** — the destructive diskutil
  verbs stay unreachable (forbidden tokens), consistent with the "exclude
  low-value destructive ops" precedent.
- **No `tmutil` restore or delete** — restoring a backup is a high-stakes,
  interactive flow better done in the Time Machine UI; deleting a backup is
  irrecoverable. Both are forbidden verbs.
- **`diskutil -plist` parsing** was considered for `list_volumes`/`volume_info`
  but not adopted: the human-readable output is already compact and clear, and a
  plist parser is extra surface for no user-visible gain. Revisit only if a
  structured consumer needs it.

## Manual on-device smoke test still owed

The read paths are safe to run live (`MCP_STORAGE_LIVE=1`). The mutating paths
need a manual stage→execute check on real hardware: attach a real `.dmg` and
confirm it mounts, detach it, and mount/unmount a real external volume — verifying
the previews name the correct manual reversal and that no undo token is offered.
