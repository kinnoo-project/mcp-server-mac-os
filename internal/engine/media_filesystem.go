// media_filesystem.go implements the filesystem domain's media & document
// conversion capabilities — the "convert/resize this image, preview this file"
// group built on macOS's own sips, textutil, and qlmanage tools.
//
// It contributes one read-only builtin and four mutators:
//
//   - image_info (builtin)          — report an image's pixel dimensions/format/etc.
//   - convert_image (mutator)       — sips format conversion to a NEW file
//   - resize_image (mutator)        — sips resample to a NEW file
//   - convert_document (mutator)    — textutil format conversion to a NEW file
//   - quicklook_thumbnail (mutator) — qlmanage preview into a server temp folder
//
// # Injection posture
//
// sips has NO usable "--" end-of-options terminator (it parses "--" as an
// unknown function and aborts), so the sole defense for sips paths is up-front
// validation: every path is rejected if it begins with "-" and is resolved to an
// ABSOLUTE path (which therefore starts with "/") before it reaches argv, so it
// can never be read as a flag. textutil and qlmanage DO honour "--", so their
// source path additionally rides after a "--" terminator as defense in depth.
// Every "format" is a closed enum the registry validates before the mutator
// runs, so it carries no injection surface. See media_filesystem_test.go for the
// per-operation regressions.
//
// # No-overwrite discipline
//
// The three converters each write a brand-NEW destination file and refuse a path
// that already exists, exactly like write_file. That is what makes their inverse
// safe: undo moves ONLY the file this operation created to the Trash (the
// project recycling rule, transactional-state.md §3), never any pre-existing
// user data.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// maxImageDimension bounds a resize target so a typo cannot ask sips to
// resample to an absurd pixel size (which would burn memory and disk). 20000 px
// comfortably exceeds any real display or print need while staying well clear of
// a resource-exhaustion request.
const maxImageDimension = 20000

// maxThumbnailSize bounds the qlmanage thumbnail dimension. Quick Look previews
// are inherently small; 2048 px covers a Retina-sized preview and caps the work.
const maxThumbnailSize = 2048

// defaultThumbnailSize is used when the caller omits an explicit size.
const defaultThumbnailSize = 512

// runImageInfo reports an image's properties via `sips --getProperty all`.
//
// The path is validated by validateExistingOperand first: sips has no "--"
// terminator, so rejecting a dash-leading value and resolving the path to
// absolute form (it then starts with "/") is what keeps it from being read as a
// sips option. The output is passed through the shared 32 KB compaction, though
// a property dump is always small.
func runImageInfo(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	raw, _ := getString(in, "path")
	path, err := validateExistingOperand("image_info", "path", raw)
	if err != nil {
		return "", err
	}
	bin, err := policy.ResolveBinary("sips")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "--getProperty", "all", path)
	if err != nil {
		return "", err
	}
	// sips echoes the file path as the first output line and then indents each
	// property; strip that leading path line so the report is not redundant with
	// the header we add.
	body := strings.TrimSpace(res.Stdout)
	if nl := strings.IndexByte(body, '\n'); nl >= 0 && strings.TrimSpace(body[:nl]) == path {
		body = strings.TrimLeft(body[nl+1:], "\n")
	}
	if body == "" {
		return "", fmt.Errorf("image_info: sips returned no properties for %q (is it a readable image?)", path)
	}
	return fmt.Sprintf("Image properties for %s:\n\n%s", path, compactOutput(body)), nil
}

// validateNewOutputPath applies the standard guardrails to a destination path
// that must NOT already exist: it rejects an empty or dash-leading value,
// resolves the path to absolute form (so any inverse built from it is stable and
// it can never be read as a flag), proves nothing already occupies the path
// (os.Lstat, so even a dangling symlink counts as occupied — writing through one
// would land the file somewhere the plan never previewed), and confirms the
// parent directory exists (the conversion tools do not create intermediate
// directories, so a plan that cannot execute must fail at stage time, not later).
func validateNewOutputPath(op, field, raw string) (string, error) {
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
	if _, err := os.Lstat(abs); err == nil {
		return "", fmt.Errorf("%s: %s %q already exists; refusing to overwrite (choose a new path, or remove it first)", op, field, abs)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("%s: cannot inspect %s %q: %w", op, field, abs, err)
	}
	parent := filepath.Dir(abs)
	if info, err := os.Stat(parent); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s: parent directory %q does not exist (create it first with mkdir)", op, parent)
		}
		return "", fmt.Errorf("%s: cannot inspect parent %q: %w", op, parent, err)
	} else if !info.IsDir() {
		return "", fmt.Errorf("%s: parent %q is not a directory", op, parent)
	}
	return abs, nil
}

// stageConvertImage stages a reversible image-format conversion with sips.
//
// Forward is `sips -s format <fmt> <source> --out <destination>`: the source is
// a positional operand and the destination is the value of --out, so neither can
// be parsed as a flag. sips has no "--" terminator, so the source's safety rests
// entirely on validateExistingOperand rejecting a dash-leading value and
// resolving it absolute. Inverse honours the recycling rule: undo moves the
// created destination to the Trash. Because staging proved the destination did
// not exist beforehand, undo can only ever trash the file this operation made.
func stageConvertImage(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	srcRaw, _ := getString(in, "source")
	src, err := validateExistingOperand("convert_image", "source", srcRaw)
	if err != nil {
		return nil, err
	}
	dstRaw, _ := getString(in, "destination")
	dst, err := validateNewOutputPath("convert_image", "destination", dstRaw)
	if err != nil {
		return nil, err
	}
	// format is a registry-validated enum, so it is guaranteed to be one of the
	// allowed values and carries no injection surface.
	format, _ := getString(in, "format")
	trashDest, err := trashPathFor(dst)
	if err != nil {
		return nil, fmt.Errorf("convert_image: %w", err)
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Convert %s to %s format, writing a new file at %s. Undo will move the created file to the Trash (%s).",
			src, format, dst, trashDest),
		Forward: Command{Binary: "sips", Args: []string{"-s", "format", format, src, "--out", dst}},
		Inverse: &Command{Binary: "mv", Args: []string{"--", dst, trashDest}},
	}, nil
}

// stageResizeImage stages a reversible image resize with sips.
//
// Exactly one of width, height, or max_dimension must be supplied; the mutator
// maps it to sips's matching resample flag (--resampleWidth / --resampleHeight /
// --resampleHeightWidthMax) and the unspecified axis scales to preserve the
// aspect ratio. Forcing --out to a NEW path is deliberate: sips resamples IN
// PLACE when --out is omitted, which would irreversibly mutate the original — so
// the mutator always writes a new file and never touches the source. Injection
// posture and the Trash inverse mirror stageConvertImage.
func stageResizeImage(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	srcRaw, _ := getString(in, "source")
	src, err := validateExistingOperand("resize_image", "source", srcRaw)
	if err != nil {
		return nil, err
	}
	dstRaw, _ := getString(in, "destination")
	dst, err := validateNewOutputPath("resize_image", "destination", dstRaw)
	if err != nil {
		return nil, err
	}
	flag, value, dimDesc, err := resizeDimension(in)
	if err != nil {
		return nil, err
	}
	trashDest, err := trashPathFor(dst)
	if err != nil {
		return nil, fmt.Errorf("resize_image: %w", err)
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Resize %s (%s), writing a new file at %s. Undo will move the created file to the Trash (%s).",
			src, dimDesc, dst, trashDest),
		Forward: Command{Binary: "sips", Args: []string{flag, strconv.Itoa(value), src, "--out", dst}},
		Inverse: &Command{Binary: "mv", Args: []string{"--", dst, trashDest}},
	}, nil
}

// resizeDimension resolves the single dimension parameter a resize must carry
// and returns the sips flag, the (bounds-checked) pixel value, and a
// human-readable description for the preview. It enforces that EXACTLY ONE of
// width/height/max_dimension is provided so the resize is unambiguous, and that
// the value is within 1..maxImageDimension.
func resizeDimension(in map[string]any) (flag string, value int, desc string, err error) {
	type dim struct {
		field string
		flag  string
		desc  string
	}
	// Order is fixed so the "provide exactly one" error is deterministic.
	dims := []dim{
		{"width", "--resampleWidth", "width"},
		{"height", "--resampleHeight", "height"},
		{"max_dimension", "--resampleHeightWidthMax", "longest side"},
	}
	var chosen *dim
	for i := range dims {
		if v, ok := getInt(in, dims[i].field); ok {
			if chosen != nil {
				return "", 0, "", fmt.Errorf("resize_image: provide exactly one of width, height, or max_dimension (got both %s and %s)", chosen.field, dims[i].field)
			}
			chosen = &dims[i]
			value = v
		}
	}
	if chosen == nil {
		return "", 0, "", fmt.Errorf("resize_image: one of width, height, or max_dimension is required")
	}
	if value < 1 || value > maxImageDimension {
		return "", 0, "", fmt.Errorf("resize_image: %s must be between 1 and %d pixels, got %d", chosen.field, maxImageDimension, value)
	}
	return chosen.flag, value, fmt.Sprintf("%s → %d px", chosen.desc, value), nil
}

// stageConvertDocument stages a reversible document conversion with textutil.
//
// Forward is `textutil -convert <fmt> -output <destination> -- <source>`.
// textutil honours "--", so the source rides after the terminator as defense in
// depth; it is ALSO validated (dash-rejected, resolved absolute) up front like
// every other operand. The destination is the value of -output and cannot be
// parsed as a flag. Inverse moves the created destination to the Trash, safe for
// the same "destination proven absent at stage time" reason as convert_image.
func stageConvertDocument(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	srcRaw, _ := getString(in, "source")
	src, err := validateExistingOperand("convert_document", "source", srcRaw)
	if err != nil {
		return nil, err
	}
	dstRaw, _ := getString(in, "destination")
	dst, err := validateNewOutputPath("convert_document", "destination", dstRaw)
	if err != nil {
		return nil, err
	}
	format, _ := getString(in, "format") // registry-validated enum
	trashDest, err := trashPathFor(dst)
	if err != nil {
		return nil, fmt.Errorf("convert_document: %w", err)
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Convert %s to %s format, writing a new file at %s. Undo will move the created file to the Trash (%s).",
			src, format, dst, trashDest),
		Forward: Command{Binary: "textutil", Args: []string{"-convert", format, "-output", dst, "--", src}},
		Inverse: &Command{Binary: "mv", Args: []string{"--", dst, trashDest}},
	}, nil
}

// stageQuicklookThumbnail stages (for immediate auto-commit) generation of a
// Quick Look preview PNG with qlmanage.
//
// qlmanage requires its output directory (-o) to already exist and names the
// output file "<input-basename>.png" inside it. So, like print_test_page's
// scratch page, staging performs one benign server-owned write: it creates a
// fresh unique directory under /tmp/mcp-fallback to receive the thumbnail. That
// per-invocation directory gives undo a precise target — the whole directory is
// this operation's product — so the inverse moves it to the Trash (recycling
// rule), never a hard delete.
//
// Forward is `qlmanage -t -s <size> -o <outDir> -- <path>`. qlmanage honours
// "--", and the path is additionally validated (dash-rejected, absolute) up
// front. size is clamped to 1..maxThumbnailSize.
func stageQuicklookThumbnail(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	raw, _ := getString(in, "path")
	path, err := validateExistingOperand("quicklook_thumbnail", "path", raw)
	if err != nil {
		return nil, err
	}
	size := defaultThumbnailSize
	if n, ok := getInt(in, "size"); ok {
		size = n
	}
	size = clampThumbnailSize(size)

	// Benign stage-time write (mirrors print_test_page): qlmanage's -o must point
	// at an existing directory, and a unique per-call directory makes undo exact.
	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		return nil, fmt.Errorf("quicklook_thumbnail: could not prepare scratch directory: %w", err)
	}
	outDir, err := os.MkdirTemp(fallbackDir, "ql-")
	if err != nil {
		return nil, fmt.Errorf("quicklook_thumbnail: could not create thumbnail directory: %w", err)
	}
	thumb := filepath.Join(outDir, filepath.Base(path)+".png")
	trashDest, err := trashPathFor(outDir)
	if err != nil {
		return nil, fmt.Errorf("quicklook_thumbnail: %w", err)
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Generate a Quick Look thumbnail of %s (up to %d px) at %s. Undo will move the generated thumbnail folder to the Trash (%s).",
			path, size, thumb, trashDest),
		Forward: Command{Binary: "qlmanage", Args: []string{"-t", "-s", strconv.Itoa(size), "-o", outDir, "--", path}},
		Inverse: &Command{Binary: "mv", Args: []string{"--", outDir, trashDest}},
	}, nil
}

// clampThumbnailSize bounds a caller-supplied thumbnail dimension into the
// supported 1..maxThumbnailSize range. Pulled out as a pure function so the
// clamp is unit-testable without invoking qlmanage.
func clampThumbnailSize(n int) int {
	switch {
	case n < 1:
		return 1
	case n > maxThumbnailSize:
		return maxThumbnailSize
	default:
		return n
	}
}
