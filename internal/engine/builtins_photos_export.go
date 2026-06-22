// builtins_photos_export.go implements export_photo: copying one media item OUT
// of the Photos library to a file on disk so it can be opened and viewed. It is
// the bridge that lets a photo's actual pixels reach the caller, complementing the
// metadata-only reads in builtins_photos.go.
//
// # Why this is a read-only builtin, not a staged mutation
//
// export_photo never changes the Photos library and never destroys data: it only
// CREATES new files, and it creates them inside a freshly-made, uniquely-named
// directory so it can never overwrite an existing file. That is exactly the safety
// property capture_screen relies on to stay in the no-confirmation read lane, so
// export_photo follows the same model — run in-process, report where the file
// landed, and surface a denied-Automation failure as an actionable hint.
//
// # Guardrails
//
//   - The model-supplied id and destination cross into AppleScript as "--"-
//     terminated `on run argv` data (via runOsascript), so neither can be parsed as
//     an osascript option.
//   - A dash-leading destination is rejected up front (defense in depth, matching
//     capture_screen), and the export always targets a fresh empty subdirectory, so
//     no pre-existing file is ever at risk.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// exportPhotoScript exports a single media item (looked up by id) into an existing
// destination folder. argv: id, destPath, useOriginals ("true"/"false"). The
// folder is created by the Go caller before this runs; Photos writes the exported
// file(s) into it using its own filenames. A bad id makes the `whose id` lookup
// error (non-zero exit), which photosScriptError reports.
const exportPhotoScript = `on run argv
	set theId to item 1 of argv
	set destPath to item 2 of argv
	set useOrig to (item 3 of argv) is "true"
	tell application "Photos"
		set m to first media item whose id is theId
		export {m} to (POSIX file destPath) using originals useOrig
	end tell
end run`

// runExportPhoto exports one media item to disk and reports the resulting file(s).
func runExportPhoto(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	id, _ := getString(in, "id")
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("export_photo: 'id' is required (from search_photos/list_photos)")
	}
	useOriginals := getBool(in, "use_originals")

	// destination is tilde-expanded by the normalizer. A leading '-' is rejected
	// even though the value reaches osascript as "--"-terminated data, mirroring
	// capture_screen: it keeps the contract uniform and avoids a confusing path.
	destination, _ := getString(in, "destination")
	if strings.HasPrefix(destination, "-") {
		return "", fmt.Errorf("export_photo: destination %q must not begin with '-'; prefix it with ./", destination)
	}

	// Always export into a FRESH, empty, uniquely-named directory so the operation
	// can never overwrite an existing file — the property that keeps it in the
	// read-only lane. When destination is omitted, the system temp dir is the base.
	base := destination
	if strings.TrimSpace(base) == "" {
		base = filepath.Join(os.TempDir(), "mcp-photos-export")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("export_photo: could not create export base directory %s: %w", base, err)
	}
	outDir, err := os.MkdirTemp(base, "export-")
	if err != nil {
		return "", fmt.Errorf("export_photo: could not create export directory under %s: %w", base, err)
	}

	// On ANY failure after the directory is created, remove it (and anything
	// Photos may have partially written into it) so a failed export never leaves
	// litter behind. os.RemoveAll — not os.Remove — because a partial export can
	// leave the directory non-empty, which os.Remove refuses to delete.
	res, err := runOsascript(ctx, exportPhotoScript, id, outDir, boolText(useOriginals))
	if err != nil {
		_ = os.RemoveAll(outDir)
		return "", err
	}
	if res.ExitCode != 0 {
		_ = os.RemoveAll(outDir)
		return "", photosScriptError("export_photo", res.Stderr)
	}

	written, err := exportedFiles(outDir)
	if err != nil {
		_ = os.RemoveAll(outDir)
		return "", fmt.Errorf("export_photo: export reported success but the output could not be read: %w", err)
	}
	if len(written) == 0 {
		_ = os.RemoveAll(outDir)
		return "", fmt.Errorf("export_photo: Photos wrote no file for id %q (the item may be unavailable, e.g. still in iCloud and not downloaded)", id)
	}
	return reportExport(outDir, written), nil
}

// exportedFile is one file Photos wrote during an export, with its size.
type exportedFile struct {
	name string
	size int64
}

// exportedFiles lists the regular files Photos wrote into outDir, sorted by name
// for stable output.
func exportedFiles(outDir string) ([]exportedFile, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, err
	}
	var out []exportedFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, exportedFile{name: e.Name(), size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// reportExport renders the success summary: the directory and each file written
// with its human-readable size, so the caller can open the path to view it.
func reportExport(outDir string, files []exportedFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Exported %d file(s) to %s\n", len(files), outDir)
	for _, f := range files {
		fmt.Fprintf(&b, "  - %s  (%s)\n      %s\n", f.name, formatBytes(f.size), filepath.Join(outDir, f.name))
	}
	return b.String()
}

// boolText renders a Go bool as the lowercase "true"/"false" the export script
// compares against in its argv.
func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
