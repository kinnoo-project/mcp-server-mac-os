// builtins_storage.go implements the read-only half of the `storage` domain (V7)
// — the operations that answer "what backups and drives does this Mac have, and
// how do I safely remove one?":
//
//   - time_machine_status — is a backup running, and when was the last one? (tmutil)
//   - list_backups        — which Time Machine restore points exist? (tmutil)
//   - list_volumes        — what disks/volumes are attached? (diskutil list)
//   - volume_info         — details of one disk/volume (diskutil info)
//   - eject_volume        — the ADVISORY op: never ejects, only hands back the
//     exact `diskutil eject` command plus a warning.
//
// # Why these binaries are usable despite their power
//
// tmutil, diskutil, and hdiutil were on the registry deny list (they can delete
// backups, erase disks, reformat volumes). V7 removes them (see
// internal/registry/security_invariants_test.go) and confines each to a closed
// set of read-only / benign verbs, exactly as V5 did for csrutil and spctl. Every
// argv is assembled by a tiny pure builder function here (tmutilStatusArgs,
// diskutilInfoArgs, …) so the verb-pinning invariant TestSecurity_ConstrainedBinaryVerbs
// can prove — without launching anything — that a storage capability's command can
// never reach `diskutil erase`, `tmutil delete`, or `hdiutil create`. The mutating
// half (mount/attach/detach) lives in mutate_storage.go and shares those pins.
//
// # The advisory pattern (eject_volume)
//
// Physically ejecting a disk while an app has files open on it can interrupt that
// app or corrupt in-flight writes, and picking the wrong identifier ejects the
// wrong disk. So eject_volume deliberately does NOT run `diskutil eject`. It is a
// read-only op that validates the target exists (a read-only `diskutil info`
// probe) and then returns the exact command for the human to run themselves, with
// a consequences warning. This is the "advisory command" pattern: the server
// instructs, the human executes.
//
// # Injection posture
//
// volume_info and eject_volume take a model-controlled volume identifier.
// diskutil has no usable "--" end-of-options terminator, so the sole defense is
// validateVolumeIdentifier: it rejects a dash-leading value and confines the
// input to a strict allowlist — either a disk identifier (diskN, diskNsM) or a
// /Volumes/<name> mount path with no embedded slash — so a hostile value can
// never be read as a diskutil flag. The tmutil-backed reads take no input at all.
package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// diskIdentifierPattern matches a bare BSD disk identifier: "disk" followed by a
// device number and zero or more "sN" slice/partition suffixes (disk0, disk2s1,
// disk1s1s1 for APFS synthesized volumes). It anchors both ends so nothing else
// — a flag, a path with slashes, a metacharacter — can slip through.
var diskIdentifierPattern = regexp.MustCompile(`^disk[0-9]+(s[0-9]+)*$`)

// volumeMountPattern matches a mount path under /Volumes with a single trailing
// name component and no further slashes, so a value can never traverse into an
// arbitrary path (e.g. "/Volumes/../etc"). The name charset excludes '/', control
// characters, and shell metacharacters; spaces are allowed because volume names
// routinely contain them (e.g. "/Volumes/Macintosh HD").
var volumeMountPattern = regexp.MustCompile(`^/Volumes/[A-Za-z0-9 ._+()-]+$`)

// validateVolumeIdentifier is the shared input guardrail for every storage
// operation that names a disk or volume (volume_info, eject_volume, mount_volume,
// detach_disk_image). diskutil and hdiutil have no "--" terminator, so this
// allowlist is the option-injection defense: it rejects empty and dash-leading
// values and confines the input to a disk identifier (diskN / diskNsM) or a
// single-component /Volumes mount path — nothing that could be read as a flag or
// traverse the filesystem. It returns the value unchanged (already argv-safe) so
// callers keep the exact string the user gave, which is what diskutil expects.
func validateVolumeIdentifier(op, field, raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%s: '%s' is required", op, field)
	}
	if strings.HasPrefix(raw, "-") {
		return "", fmt.Errorf("%s: %s %q begins with '-' and is not allowed (it would be read as a command-line option)", op, field, raw)
	}
	// Reject the special path components "." and ".." as the /Volumes name.
	// volumeMountPattern's charset includes '.', so "/Volumes/.." (which
	// normalizes to "/") and "/Volumes/." (to "/Volumes") would otherwise slip
	// through and defeat the single-component traversal defense. A literal name
	// that merely CONTAINS dots (e.g. "/Volumes/My.Disk") is fine — only the two
	// pure dot-components traverse.
	if raw == "/Volumes/." || raw == "/Volumes/.." {
		return "", fmt.Errorf("%s: %s %q is not a specific volume; give a disk identifier like disk2 or a named mount path like /Volumes/Backup", op, field, raw)
	}
	if diskIdentifierPattern.MatchString(raw) || volumeMountPattern.MatchString(raw) {
		return raw, nil
	}
	return "", fmt.Errorf(
		"%s: %s %q is not a valid volume identifier; give a disk identifier like disk2 or disk2s1, or a mount path like /Volumes/Backup",
		op, field, raw)
}

// tmutilStatusArgs / tmutilLatestBackupArgs / tmutilListBackupsArgs /
// tmutilListSnapshotsArgs are the pinned, input-free argument vectors for the
// Time Machine reads. tmutil is confined to these four read-only verbs and can
// never reach a state-changing one (delete, restore, deletelocalsnapshots,
// setdestination). Kept as pure functions so the verb-pinning invariant can
// assert the shapes without invoking tmutil.
func tmutilStatusArgs() []string       { return []string{"status"} }
func tmutilLatestBackupArgs() []string { return []string{"latestbackup"} }
func tmutilListBackupsArgs() []string  { return []string{"listbackups"} }

// tmutilListSnapshotsArgs lists the LOCAL hourly snapshots kept on the startup
// volume ("/"). The "/" is a fixed constant, never model input.
func tmutilListSnapshotsArgs() []string { return []string{"listlocalsnapshots", "/"} }

// diskutilListArgs is the pinned argv for list_volumes: diskutil's read-only
// "list" verb with no model input.
func diskutilListArgs() []string { return []string{"list"} }

// diskutilInfoArgs is the pinned argv for volume_info (and the eject_volume
// existence probe): diskutil's read-only "info" verb followed by the already
// validated identifier. diskutil has no "--" terminator, so the identifier's
// safety rests entirely on validateVolumeIdentifier having run first.
func diskutilInfoArgs(volume string) []string { return []string{"info", volume} }

// runTimeMachineStatus reports whether a Time Machine backup is in progress and
// when the last completed backup ran. It combines two read-only tmutil calls:
// `status` (the live backup phase/progress) and `latestbackup` (the newest
// completed snapshot's path, whose trailing component is a date). `latestbackup`
// exits non-zero when no backups exist yet — that is a normal "never backed up"
// answer, surfaced as data, not an error.
func runTimeMachineStatus(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("tmutil")
	if err != nil {
		return "", err
	}
	statusRes, err := runCommand(ctx, bin, tmutilStatusArgs()...)
	if err != nil {
		return "", err
	}
	latestRes, err := runCommand(ctx, bin, tmutilLatestBackupArgs()...)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("Time Machine status:\n")
	if s := strings.TrimSpace(statusRes.Stdout); s != "" {
		b.WriteString(s)
	} else {
		b.WriteString("(tmutil reported no status)")
	}
	b.WriteString("\n\nMost recent completed backup:\n")
	if latestRes.ExitCode == 0 {
		if latest := strings.TrimSpace(latestRes.Stdout); latest != "" {
			b.WriteString(latest)
		} else {
			b.WriteString("(none reported)")
		}
	} else {
		// Non-zero exit here is the common "no backups / Time Machine not set up"
		// case; tmutil explains it on stderr. Report it as the answer.
		detail := strings.TrimSpace(latestRes.Stderr)
		if detail == "" {
			detail = "no completed backups were found (Time Machine may not be configured)"
		}
		b.WriteString(detail)
	}
	return compactOutput(b.String()), nil
}

// runListBackups lists the Time Machine restore points available: the full
// backups on the backup destination (`tmutil listbackups`) and the local hourly
// snapshots on the startup disk (`tmutil listlocalsnapshots /`). Either can exit
// non-zero (no destination configured, no local snapshots), which is reported as
// data so "you have no backups" reads as the answer rather than a failure.
func runListBackups(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("tmutil")
	if err != nil {
		return "", err
	}
	var b strings.Builder

	backupsRes, err := runCommand(ctx, bin, tmutilListBackupsArgs()...)
	if err != nil {
		return "", err
	}
	b.WriteString("Time Machine backups (on the backup disk):\n")
	b.WriteString(tmutilListOrExplain(backupsRes, "no backups on a backup disk were found (Time Machine may not be configured, or the backup disk is not connected)"))

	snapsRes, err := runCommand(ctx, bin, tmutilListSnapshotsArgs()...)
	if err != nil {
		return "", err
	}
	b.WriteString("\n\nLocal snapshots (on this Mac's startup disk):\n")
	b.WriteString(tmutilListOrExplain(snapsRes, "no local snapshots are currently kept"))

	return compactOutput(b.String()), nil
}

// tmutilListOrExplain renders a tmutil listing result: its stdout when the call
// succeeded and printed something, otherwise a friendly explanation (drawn from
// stderr when tmutil offered one, else the supplied fallback). Pulled out so both
// listings in runListBackups treat the "nothing to list" case identically.
func tmutilListOrExplain(res *runResult, fallback string) string {
	if res.ExitCode == 0 {
		if out := strings.TrimSpace(res.Stdout); out != "" {
			return out
		}
	}
	if detail := strings.TrimSpace(res.Stderr); detail != "" {
		return detail
	}
	return fallback
}

// runListVolumes lists every attached disk and volume via `diskutil list`. Fixed
// argv, no model input, no injection surface.
func runListVolumes(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("diskutil")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, diskutilListArgs()...)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		out = strings.TrimSpace(res.Stderr)
	}
	if out == "" {
		return "diskutil reported no attached disks or volumes.", nil
	}
	return "Attached disks and volumes:\n\n" + compactOutput(out), nil
}

// runVolumeInfo reports detailed information about one disk or volume via
// `diskutil info`. The identifier is validated by validateVolumeIdentifier first
// (diskutil has no "--" terminator, so the allowlist is the sole option-injection
// defense). A non-zero exit — "could not find disk" for a typo'd identifier — is
// surfaced as an error so the caller learns the volume does not exist rather than
// getting a misleadingly empty report.
func runVolumeInfo(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	raw, _ := getString(in, "volume")
	volume, err := validateVolumeIdentifier("volume_info", "volume", raw)
	if err != nil {
		return "", err
	}
	bin, err := policy.ResolveBinary("diskutil")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, diskutilInfoArgs(volume)...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		if detail == "" {
			detail = fmt.Sprintf("diskutil info exited with status %d", res.ExitCode)
		}
		return "", fmt.Errorf("volume_info: could not read %q: %s", volume, detail)
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		return "", fmt.Errorf("volume_info: diskutil returned no information for %q", volume)
	}
	return fmt.Sprintf("Information for %s:\n\n%s", volume, compactOutput(out)), nil
}

// runEjectVolume is the ADVISORY operation: it never ejects anything. It confirms
// the named volume exists (a read-only `diskutil info` probe) and then returns the
// exact `diskutil eject` command for the human to run, with a consequences
// warning. Ejecting is left to the human precisely because doing it under an app
// that has files open can interrupt that app or corrupt writes, and an
// automatically-fired eject aimed at the wrong identifier is hard to take back.
// This mirrors how the tool description promises "this never ejects, it only
// instructs".
func runEjectVolume(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	raw, _ := getString(in, "volume")
	volume, err := validateVolumeIdentifier("eject_volume", "volume", raw)
	if err != nil {
		return "", err
	}
	bin, err := policy.ResolveBinary("diskutil")
	if err != nil {
		return "", err
	}
	// Read-only existence probe. If the identifier does not resolve, tell the
	// caller rather than handing back an eject command for a disk that is not
	// there (which would only invite them to run something that fails).
	res, err := runCommand(ctx, bin, diskutilInfoArgs(volume)...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		if detail == "" {
			detail = "no such disk or volume"
		}
		return "", fmt.Errorf("eject_volume: cannot find %q to eject: %s", volume, detail)
	}
	return fmt.Sprintf(
		"To eject %s, run this yourself in Terminal:\n\n    diskutil eject %s\n\n"+
			"This server does NOT eject the disk for you — ejecting is left to you on purpose. "+
			"Before you run it, make sure no files on this volume are open in any app: ejecting "+
			"while a file is in use can interrupt that app or corrupt an in-progress write, and "+
			"ejecting the wrong disk can disrupt anything relying on it. If a Finder window or an "+
			"app still has the disk open, close it first, then run the command above.",
		volume, volume), nil
}
