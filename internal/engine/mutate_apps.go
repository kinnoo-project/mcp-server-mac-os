// mutate_apps.go implements the mutating Application operations: launching,
// focusing, and quitting an app.
//
// # Lanes
//
//   - open_application and focus_application are AUTO-COMMIT (registry-marked):
//     they are benign, everyday actions that run immediately instead of waiting
//     behind the execute token. open_application is reversible (its inverse quits
//     the app — but only when staging observed the app was NOT already running,
//     so an undo never quits something the user already had open). focus has no
//     meaningful inverse.
//   - quit_application is STAGED like every other potentially-lossy mutation:
//     unsaved work could be lost and relaunching is not a true reversal, so a
//     human confirms via execute and no undo is offered.
//
// # Why the app name is safe
//
// The launch path builds `open -a <name>` argv directly (no shell), and the
// focus/quit paths pass <name> as DATA after the osascript "--" terminator
// (applescript.go), so neither shell nor osascript option injection is possible.
// validateAppName adds a thin sanity check (non-empty, no control characters, no
// leading dash) on top of that structural safety.
package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// activateScript brings the named application to the front (launching it if
// necessary). The name arrives as data in argv item 1.
const activateScript = `on run argv
	tell application (item 1 of argv) to activate
end run`

// quitScript quits the named application. The name arrives as data in argv item 1.
const quitScript = `on run argv
	tell application (item 1 of argv) to quit
end run`

// stageOpenApplication stages (for immediate auto-commit) launching an app. The
// forward command is `open -a <name>`, which both launches a stopped app and
// brings a running one to the front. Whether an undo is offered depends on
// whether the app is already running: if it was NOT already running, the inverse
// quits it; if it was already running (or we could not determine state), no
// inverse is set, so undo can never quit an app the user already had open.
//
// The running check uses Launch Services (`lsappinfo`), NOT a System Events
// AppleScript probe. The AppleScript route would trip an Automation permission
// prompt on first use, which would block this otherwise-low-friction auto-commit
// operation; `lsappinfo` needs no such grant.
func stageOpenApplication(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	name, err := validateAppName("open_application", in)
	if err != nil {
		return nil, err
	}

	forward := Command{Binary: "open", Args: []string{"-a", name}}

	plan := &StagedPlan{
		Forward: forward,
	}
	if appAlreadyRunning(ctx, name) {
		plan.Preview = fmt.Sprintf("Open %q (it appears to be running already, so it will simply be brought to the front).", name)
		plan.Inverse = nil
	} else {
		plan.Preview = fmt.Sprintf("Launch %q. Undo will quit it again.", name)
		inverse := osascriptCommand(quitScript, name)
		plan.Inverse = &inverse
	}
	return plan, nil
}

// appAlreadyRunning reports whether an application matching name is currently
// running, using Launch Services via `lsappinfo list` (no Automation grant
// required, unlike a System Events probe).
//
// It deliberately biases toward "running" on any uncertainty — an unreadable
// listing, or a name we cannot confidently match returns true — because the only
// consequence of a true result is that open_application offers no undo. A false
// "stopped" would be the dangerous error: it would let undo quit an app the user
// already had open, so we never guess in that direction.
func appAlreadyRunning(ctx context.Context, name string) bool {
	bin, err := policy.ResolveBinary("lsappinfo")
	if err != nil {
		return true // cannot check → assume running → no undo offered
	}
	res, err := runCommand(ctx, bin, "list")
	if err != nil || res.ExitCode != 0 {
		return true
	}
	return lsappinfoListsApp(res.Stdout, name)
}

// lsappinfoListsApp is the pure matching half of appAlreadyRunning, split out so
// it can be unit-tested against captured `lsappinfo list` output. It reports
// whether name (an `open -a` target — a display name or a bundle name, matched
// case-insensitively like `open -a` itself) appears among the running apps.
//
// `lsappinfo list` prints, per running app, a header line carrying the quoted
// display name (e.g. `12) "Safari" ASN:...`) and a `bundle path="/…/Safari.app"`
// line. We collect both forms — display name and `.app` bundle basename — so a
// caller passing either spelling matches.
func lsappinfoListsApp(stdout, name string) bool {
	want := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, ".app")))
	if want == "" {
		return true // nothing sensible to match → err toward "running"
	}
	for _, line := range splitNonEmptyLines(stdout) {
		line = strings.TrimSpace(line)
		// Display name: the first double-quoted token on a header line.
		if open := strings.Index(line, `"`); open >= 0 {
			if close := strings.Index(line[open+1:], `"`); close >= 0 {
				display := line[open+1 : open+1+close]
				if strings.EqualFold(display, want) {
					return true
				}
			}
		}
		// Bundle path: match the `.app` bundle's basename.
		if strings.HasPrefix(line, `bundle path="`) {
			path := strings.TrimSuffix(strings.TrimPrefix(line, `bundle path="`), `"`)
			base := strings.TrimSuffix(filepath.Base(path), ".app")
			if strings.EqualFold(base, want) {
				return true
			}
		}
	}
	return false
}

// stageFocusApplication stages (for immediate auto-commit) bringing an app to the
// front. Focus has no meaningful inverse, so the plan is irreversible.
func stageFocusApplication(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	name, err := validateAppName("focus_application", in)
	if err != nil {
		return nil, err
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Bring %q to the front.", name),
		Forward: osascriptCommand(activateScript, name),
		Inverse: nil,
	}, nil
}

// stageQuitApplication stages quitting an app through the confirmation gate.
// Quitting can lose unsaved work and relaunching is not a true reversal, so the
// plan is irreversible and routes through execute (it is NOT auto-commit).
func stageQuitApplication(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	name, err := validateAppName("quit_application", in)
	if err != nil {
		return nil, err
	}
	return &StagedPlan{
		Preview: fmt.Sprintf("Quit %q. Any unsaved work in that app may be lost, and this cannot be automatically undone.", name),
		Forward: osascriptCommand(quitScript, name),
		Inverse: nil,
	}, nil
}

// validateAppName extracts and sanity-checks the "name" parameter shared by every
// application mutator. The structural injection defences live elsewhere (argv
// splitting and the osascript "--" terminator); this rejects values that are
// obviously not an application name so the failure is a clear validation error
// rather than a confusing AppleScript or `open` error later.
func validateAppName(op string, in map[string]any) (string, error) {
	name, _ := getString(in, "name")
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%s: 'name' is required", op)
	}
	// A leading dash would be unusual for an app name and risks being read as a
	// flag by `open`; reject it rather than rely on positional consumption.
	if strings.HasPrefix(name, "-") {
		return "", fmt.Errorf("%s: application name %q must not begin with '-'", op, name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s: application name must not contain control characters", op)
		}
	}
	return name, nil
}
