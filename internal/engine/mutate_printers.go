// mutate_printers.go implements the printing mutators: print_file and
// print_test_page. Both are STAGED (never auto-commit) and irreversible — paper
// and ink cannot be un-consumed, so there is no inverse and a human confirms via
// the execute step, exactly like placing a call or sending mail.
//
// # The test page and the one benign stage-time write
//
// macOS does not ship a CUPS test page file, so print_test_page carries its own:
// a small text page embedded into the binary. A `lp` command can only print a
// file that exists on disk, so staging writes that embedded page to a server-
// owned scratch file under /tmp/mcp-fallback/ (the staging directory blessed by
// transactional-state.md §3) and the forward command prints THAT path. Writing
// this scratch file is the single, deliberate exception to "staging performs no
// side effects": it touches only a temp file this server owns, never any user or
// system state, and the actual print still waits for execute.
package engine

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mcp-server-mac-os/internal/registry"
)

// testPageContent is the built-in test page, embedded so the binary is fully
// self-contained and print_test_page needs no file on disk to exist beforehand.
//
//go:embed testpage.txt
var testPageContent string

// fallbackDir is the server-owned scratch directory used for staged artifacts
// such as the generated test page. It mirrors the staging path recommended by
// transactional-state.md §3.
const fallbackDir = "/tmp/mcp-fallback"

// maxPrintCopies bounds the copies parameter so a typo cannot launch a runaway
// print job.
const maxPrintCopies = 20

// stagePrintFile stages an irreversible print of a user-supplied file. The file
// path arrives already tilde-expanded (TypePath normalization); staging confirms
// it exists and is a regular file before building the `lp` command, so a missing
// path fails loudly at stage time rather than as a confusing lp error on execute.
func stagePrintFile(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	file, _ := getString(in, "file")
	if strings.TrimSpace(file) == "" {
		return nil, fmt.Errorf("print_file: 'file' is required")
	}
	if strings.HasPrefix(file, "-") {
		return nil, fmt.Errorf("print_file: path %q begins with '-' and is not allowed; prefix it with ./", file)
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, fmt.Errorf("print_file: cannot read %q: %w", file, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("print_file: %q is a directory, not a file", file)
	}

	printer, err := validatePrinterName("print_file", in)
	if err != nil {
		return nil, err
	}
	copies, err := validateCopies(in)
	if err != nil {
		return nil, err
	}

	args := lpArgs(printer, copies, file)
	return &StagedPlan{
		Preview: fmt.Sprintf("Print %s%s%s. This consumes paper/ink and cannot be undone.",
			file, printerPhrase(printer), copiesPhrase(copies)),
		Forward: Command{Binary: "lp", Args: args},
		Inverse: nil,
	}, nil
}

// stagePrintTestPage stages an irreversible print of the built-in test page. It
// writes the embedded page to the scratch file described in the file header and
// stages a `lp` command that prints it.
func stagePrintTestPage(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	printer, err := validatePrinterName("print_test_page", in)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		return nil, fmt.Errorf("print_test_page: could not prepare scratch directory: %w", err)
	}
	path := filepath.Join(fallbackDir, "testpage.txt")
	if err := os.WriteFile(path, []byte(testPageContent), 0o644); err != nil {
		return nil, fmt.Errorf("print_test_page: could not write the test page: %w", err)
	}

	args := lpArgs(printer, 1, path)
	return &StagedPlan{
		Preview: fmt.Sprintf("Print a built-in test page%s. This consumes paper/ink and cannot be undone.", printerPhrase(printer)),
		Forward: Command{Binary: "lp", Args: args},
		Inverse: nil,
	}, nil
}

// lpArgs assembles the `lp` argument vector deterministically: an optional
// destination, an optional copy count (only when greater than one), then the
// "--" terminator and the file path so a path can never be parsed as a flag.
func lpArgs(printer string, copies int, file string) []string {
	var args []string
	if printer != "" {
		args = append(args, "-d", printer)
	}
	if copies > 1 {
		args = append(args, "-n", itoa(copies))
	}
	args = append(args, "--", file)
	return args
}

// validatePrinterName reads and sanity-checks the optional "printer" parameter.
// An empty value means "use the default printer". A leading dash or control
// character is rejected so the name can never be mistaken for an lp option.
func validatePrinterName(op string, in map[string]any) (string, error) {
	name, _ := getString(in, "printer")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	if strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("%s: printer name %q must not begin with '-'", op, name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s: printer name must not contain control characters", op)
		}
	}
	return name, nil
}

// validateCopies reads the optional "copies" parameter, defaulting to 1 and
// enforcing the 1..maxPrintCopies bound.
func validateCopies(in map[string]any) (int, error) {
	copies, ok := getInt(in, "copies")
	if !ok {
		return 1, nil
	}
	if copies < 1 || copies > maxPrintCopies {
		return 0, fmt.Errorf("print_file: 'copies' must be between 1 and %d", maxPrintCopies)
	}
	return copies, nil
}

// printerPhrase renders the destination clause for a preview ("" → default).
func printerPhrase(printer string) string {
	if printer == "" {
		return " on the default printer"
	}
	return fmt.Sprintf(" on %q", printer)
}

// copiesPhrase renders the copy-count clause for a preview (omitted for 1).
func copiesPhrase(copies int) string {
	if copies > 1 {
		return fmt.Sprintf(" (%d copies)", copies)
	}
	return ""
}
