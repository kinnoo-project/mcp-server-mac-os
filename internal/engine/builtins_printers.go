// builtins_printers.go implements the read-only printer status builtins:
// list_printers (which printers exist, their state, and the default) and
// list_print_jobs (what is queued). These are the "is my printer there and
// working?" reads that pair with the staged print operations in
// mutate_printers.go.
//
// macOS printing is CUPS, and CUPS' status tools (lpstat) emit human-oriented
// text with no machine-readable (-json) mode, so a small tolerant parser turns
// that text into a clean summary. Adding/connecting or re-enabling a printer
// needs administrator rights (lpadmin/cupsenable) that cannot be obtained over
// this server's non-interactive transport, so those are intentionally NOT here:
// list_printers instead flags a disabled queue and points at open_settings.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// runListPrinters reports the configured printers, their state, and the default.
func runListPrinters(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("lpstat")
	if err != nil {
		return "", err
	}
	// -p lists each printer with its enabled/disabled state; -d names the system
	// default. lpstat exits non-zero when no printers are configured, which is a
	// legitimate "nothing to show" answer rather than a failure.
	res, err := runCommand(ctx, bin, "-p", "-d")
	if err != nil {
		return "", err
	}
	return renderPrinterList(res.Stdout), nil
}

// printerState is one configured printer parsed out of lpstat -p output.
type printerState struct {
	name     string // the queue name, e.g. "HP_LaserJet_Pro_M148fdw"
	detail   string // the human description after the name, e.g. "is idle. enabled since ..."
	disabled bool   // true when the queue is disabled (jobs will not print)
}

// renderPrinterList is the pure parsing/formatting half of runListPrinters, split
// out so it can be unit-tested without invoking lpstat. It understands the two
// lpstat line shapes it cares about: "printer <name> <state...>" and the
// "system default destination: <name>" / "no system default destination" lines.
func renderPrinterList(stdout string) string {
	var printers []printerState
	var defaultName string
	for _, line := range asRows(stdout) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "printer "):
			rest := strings.TrimPrefix(line, "printer ")
			name, detail, _ := strings.Cut(rest, " ")
			printers = append(printers, printerState{
				name:     name,
				detail:   strings.TrimSpace(detail),
				disabled: strings.Contains(detail, "disabled"),
			})
		case strings.HasPrefix(line, "system default destination: "):
			defaultName = strings.TrimSpace(strings.TrimPrefix(line, "system default destination: "))
		}
	}

	if len(printers) == 0 {
		return "No printers are configured on this Mac. Add one in System Settings (you can open it with open_settings, pane 'printers')."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d printer(s) configured:\n", len(printers))
	anyDisabled := false
	for _, p := range printers {
		marker := ""
		if p.name == defaultName {
			marker = " [default]"
		}
		fmt.Fprintf(&b, "  %s%s — %s\n", p.name, marker, p.detail)
		if p.disabled {
			anyDisabled = true
		}
	}
	if defaultName == "" {
		b.WriteString("\nThere is no system default printer set.\n")
	}
	if anyDisabled {
		b.WriteString("\nNote: a disabled printer will not print until it is re-enabled, which requires administrator rights. Open Printers settings with open_settings (pane 'printers') to re-enable or reconnect it.\n")
	}
	return b.String()
}

// runListPrintJobs reports queued print jobs across all printers.
func runListPrintJobs(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("lpstat")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "-o")
	if err != nil {
		return "", err
	}
	jobs := asRows(res.Stdout)
	if len(jobs) == 0 {
		return "No print jobs are queued.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d queued print job(s):\n", len(jobs))
	for _, j := range jobs {
		fmt.Fprintf(&b, "  %s\n", strings.TrimSpace(j))
	}
	return b.String(), nil
}
