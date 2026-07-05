// mutate_storage_test.go covers the storage domain's mutators: the forward argv
// pins for mount/attach/detach, the "no auto-undo" contract (Inverse nil on all
// three), the per-operation hostile-input regressions, and the advisory-command
// text each preview carries so a human knows how to reverse the change by hand.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestStorageMutatorArgvBuilders pins the forward argument vectors so a future
// edit cannot swap a benign verb for a destructive one (e.g. diskutil erase,
// hdiutil create) without failing here as well as in the verb-pin invariant.
func TestStorageMutatorArgvBuilders(t *testing.T) {
	if got, want := strings.Join(diskutilMountArgs("disk2s1"), " "), "mount disk2s1"; got != want {
		t.Errorf("diskutilMountArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(hdiutilAttachArgs("/tmp/x.dmg"), " "), "attach /tmp/x.dmg"; got != want {
		t.Errorf("hdiutilAttachArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(hdiutilDetachArgs("/Volumes/X"), " "), "detach /Volumes/X"; got != want {
		t.Errorf("hdiutilDetachArgs = %q, want %q", got, want)
	}
}

// TestStorageMutators_StagePlans exercises the happy path of each mutator: a
// valid input produces the pinned forward command, NO inverse (all three are
// irreversible-by-server), and a preview that names the manual reversal.
func TestStorageMutators_StagePlans(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}

	// mount_volume: forward `diskutil mount <id>`, preview points at diskutil unmount.
	plan, err := stageMountVolume(ctx, cap, map[string]any{"volume": "disk2s1"})
	if err != nil {
		t.Fatalf("stageMountVolume: %v", err)
	}
	if plan.Forward.Binary != "diskutil" || strings.Join(plan.Forward.Args, " ") != "mount disk2s1" {
		t.Errorf("mount_volume forward = %s %v, want diskutil mount disk2s1", plan.Forward.Binary, plan.Forward.Args)
	}
	if plan.Inverse != nil {
		t.Errorf("mount_volume must have no auto-undo (Inverse nil), got %+v", plan.Inverse)
	}
	if !strings.Contains(plan.Preview, "diskutil unmount disk2s1") {
		t.Errorf("mount_volume preview should give the manual unmount command, got: %s", plan.Preview)
	}

	// detach_disk_image: forward `hdiutil detach <mp>`, no inverse, preview names attach_disk_image.
	dplan, err := stageDetachDiskImage(ctx, cap, map[string]any{"mountpoint": "/Volumes/Installer"})
	if err != nil {
		t.Fatalf("stageDetachDiskImage: %v", err)
	}
	if dplan.Forward.Binary != "hdiutil" || strings.Join(dplan.Forward.Args, " ") != "detach /Volumes/Installer" {
		t.Errorf("detach forward = %s %v, want hdiutil detach /Volumes/Installer", dplan.Forward.Binary, dplan.Forward.Args)
	}
	if dplan.Inverse != nil {
		t.Errorf("detach_disk_image must have no auto-undo, got %+v", dplan.Inverse)
	}
	if !strings.Contains(dplan.Preview, "attach_disk_image") {
		t.Errorf("detach preview should point at attach_disk_image, got: %s", dplan.Preview)
	}
}

// TestStorageAttach_StagesExistingImage confirms attach_disk_image stages the
// pinned forward command for a real, existing file (resolved to an absolute
// path), with no inverse and a preview that points at detach_disk_image.
func TestStorageAttach_StagesExistingImage(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "image.dmg")
	if err := os.WriteFile(img, []byte("not a real dmg"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := stageAttachDiskImage(context.Background(), registry.Capability{}, map[string]any{"path": img})
	if err != nil {
		t.Fatalf("stageAttachDiskImage: %v", err)
	}
	if plan.Forward.Binary != "hdiutil" || len(plan.Forward.Args) != 2 || plan.Forward.Args[0] != "attach" || plan.Forward.Args[1] != img {
		t.Errorf("attach forward = %s %v, want hdiutil attach %s", plan.Forward.Binary, plan.Forward.Args, img)
	}
	if plan.Inverse != nil {
		t.Errorf("attach_disk_image must have no auto-undo, got %+v", plan.Inverse)
	}
	if !strings.Contains(plan.Preview, "detach_disk_image") {
		t.Errorf("attach preview should point at detach_disk_image, got: %s", plan.Preview)
	}
}

// TestStorageMutators_HostileInputRejected is the per-operation injection
// regression required by CLAUDE.md §4: a dash-leading identifier/path (the
// classic "-e"-as-a-flag payload) must be refused at stage time for every storage
// mutator, so it never reaches a diskutil/hdiutil argv where it could be read as
// a flag. Staging must return an error and no plan.
func TestStorageMutators_HostileInputRejected(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	for _, hostile := range []string{"-e", "-rf", "--flood", "-"} {
		if _, err := stageMountVolume(ctx, cap, map[string]any{"volume": hostile}); err == nil {
			t.Errorf("stageMountVolume(%q): expected rejection, got nil error", hostile)
		}
		if _, err := stageDetachDiskImage(ctx, cap, map[string]any{"mountpoint": hostile}); err == nil {
			t.Errorf("stageDetachDiskImage(%q): expected rejection, got nil error", hostile)
		}
		if _, err := stageAttachDiskImage(ctx, cap, map[string]any{"path": hostile}); err == nil {
			t.Errorf("stageAttachDiskImage(%q): expected rejection of a dash-leading path, got nil error", hostile)
		}
	}
}

// TestStorageAttach_NonexistentPathRejected confirms attach_disk_image fails
// cleanly for a path that does not exist (validateExistingOperand), rather than
// staging an hdiutil attach against a missing file.
func TestStorageAttach_NonexistentPathRejected(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-image.dmg")
	if _, err := stageAttachDiskImage(context.Background(), registry.Capability{}, map[string]any{"path": missing}); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("attach_disk_image on a missing path: expected a 'does not exist' error, got %v", err)
	}
}
