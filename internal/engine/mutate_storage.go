// mutate_storage.go implements the mutating half of the `storage` domain (V7):
// the operations that bring a volume online or take a disk image offline. Each
// changes what is mounted on the Mac, so all three sit on the mutation seam and
// are STAGED for human approval rather than run on sight.
//
//   - mount_volume       — mount an attached-but-unmounted volume (diskutil mount)
//   - attach_disk_image  — open a .dmg/.iso as a mounted volume (hdiutil attach)
//   - detach_disk_image  — close a mounted disk image (hdiutil detach)
//
// # Why none of them carries an auto-undo
//
// A staged inverse must be fully resolved at STAGE time (see mutate.go). For
// mount_volume and attach_disk_image the natural reversal needs a fact that does
// not exist until AFTER the forward command runs: which mount point / device the
// system assigned. hdiutil attach in particular only reveals the mount point in
// its own output. So, exactly as V3's keep_awake/allow_sleep pair resolved the
// same tension, these operations carry NO inverse and are classified irreversible;
// the reversal is a separate, explicit operation:
//
//   - to undo mount_volume the result gives the exact `diskutil unmount` command
//     for the human to run (the advisory-command pattern, like eject_volume);
//   - to undo attach_disk_image, call detach_disk_image.
//
// detach_disk_image is itself irreversible-with-no-undo: re-attaching needs the
// original image path, which detach does not carry. It is still STAGED because a
// volume disappearing is a real state change a human should confirm.
//
// # Why the inputs are safe
//
// Every model value is a disk/volume identifier validated by
// validateVolumeIdentifier (disk identifier or single-component /Volumes path,
// dash-leading rejected) or, for attach, an existing file path validated by
// validateExistingOperand (dash-leading rejected, resolved absolute). diskutil and
// hdiutil have no "--" terminator, so those allowlists ARE the option-injection
// defense; the per-operation regressions in mutate_storage_test.go feed hostile
// dash-leading values and assert they are refused.
package engine

import (
	"context"
	"fmt"

	"mcp-server-mac-os/internal/registry"
)

// diskutilMountArgs is the pinned forward argv for mount_volume: diskutil's
// "mount" verb followed by the validated identifier. Pure function so the
// verb-pinning invariant can assert it never becomes an erase/reformat verb.
func diskutilMountArgs(volume string) []string { return []string{"mount", volume} }

// hdiutilAttachArgs is the pinned forward argv for attach_disk_image: hdiutil's
// benign "attach" verb followed by the validated (existing, absolute) image path.
func hdiutilAttachArgs(path string) []string { return []string{"attach", path} }

// hdiutilDetachArgs is the pinned forward argv for detach_disk_image: hdiutil's
// "detach" verb followed by the validated mount point / device identifier.
func hdiutilDetachArgs(mountpoint string) []string { return []string{"detach", mountpoint} }

// stageMountVolume stages mounting an attached-but-unmounted volume. Forward is
// `diskutil mount <id>`. There is no inverse — the assigned mount point is not
// known until the mount happens, and a staged inverse must be resolved at stage
// time — so the plan is irreversible and the preview hands the user the exact
// `diskutil unmount` command to reverse it themselves.
func stageMountVolume(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	raw, _ := getString(in, "volume")
	volume, err := validateVolumeIdentifier("mount_volume", "volume", raw)
	if err != nil {
		return nil, err
	}
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Mount volume %s so its files become available under /Volumes. "+
				"This does not auto-undo; to unmount it afterwards, run `diskutil unmount %s` yourself "+
				"(make sure no files on it are open first).",
			volume, volume),
		Forward: Command{Binary: "diskutil", Args: diskutilMountArgs(volume)},
		Inverse: nil, // assigned mount point unknown until after the mount; reverse via `diskutil unmount`
	}, nil
}

// stageAttachDiskImage stages attaching a disk image file. Forward is
// `hdiutil attach <path.dmg>` with the path validated to exist and resolved
// absolute (so a dash-leading value can never be read as an hdiutil flag). There
// is no inverse — hdiutil reports the assigned mount point only after it attaches,
// and a staged inverse must be resolved at stage time — so the plan is
// irreversible and the preview points the user at detach_disk_image to close it.
func stageAttachDiskImage(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	raw, _ := getString(in, "path")
	path, err := validateExistingOperand("attach_disk_image", "path", raw)
	if err != nil {
		return nil, err
	}
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Attach the disk image %s so its contents mount as a volume under /Volumes. "+
				"This does not auto-undo; to close it afterwards, use the detach_disk_image operation "+
				"(or eject the mounted volume).",
			path),
		Forward: Command{Binary: "hdiutil", Args: hdiutilAttachArgs(path)},
		Inverse: nil, // assigned mount point unknown until after attach; reverse via detach_disk_image
	}, nil
}

// stageDetachDiskImage stages detaching a mounted disk image. Forward is
// `hdiutil detach <mountpoint>` with the mount point validated by the shared
// identifier allowlist (a /Volumes path or disk identifier, dash-leading
// rejected). Detaching a virtual image is benign, but a volume disappearing is a
// real state change, so it is still staged. There is no inverse — re-attaching
// needs the original image file path, which detach does not know — so the plan is
// irreversible and the preview says to re-attach with attach_disk_image.
func stageDetachDiskImage(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	raw, _ := getString(in, "mountpoint")
	mountpoint, err := validateVolumeIdentifier("detach_disk_image", "mountpoint", raw)
	if err != nil {
		return nil, err
	}
	return &StagedPlan{
		Preview: fmt.Sprintf(
			"Detach the disk image mounted at %s, unmounting its volume. "+
				"This does not auto-undo; re-open the image with attach_disk_image if you need it again. "+
				"Make sure no files on it are open first.",
			mountpoint),
		Forward: Command{Binary: "hdiutil", Args: hdiutilDetachArgs(mountpoint)},
		Inverse: nil, // original image path unknown to detach; reverse via attach_disk_image
	}, nil
}
