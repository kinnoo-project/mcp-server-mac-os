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
	"os"
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

// stageOpenFile stages opening a file, optionally in a named application (e.g. a
// PNG in Preview). Unlike open_application it is NOT auto-commit: every open waits
// behind the execute confirmation gate, so nothing launches until the user
// approves.
//
// # Two shapes
//
//   - With "app": the forward command is `open -a <app> -- <file>`. Staging asks
//     appdocs.go whether the app actually handles the file's type and folds that
//     verdict into the preview — a clean "Open X in Y" when supported, or a
//     prominent warning when it may not be (or when support could not be
//     determined). Reversibility mirrors open_application: if the app was not
//     already running, undo quits it; if it was, no undo is offered so we never
//     quit an app the user already had open.
//   - Without "app": the forward command is `open -- <file>`, which opens the file
//     in whatever application macOS has registered as the default handler for its
//     type. No support check is needed (the default handler opens the type by
//     definition; if there is none, `open` itself errors on execute), and no undo
//     is offered because staging cannot know which app will be launched.
//
// The "--" terminator keeps the path from ever being read as an `open` option;
// validateAppNameValue and the explicit dash-leading file check below add the same
// belt-and-suspenders guards the other path-bearing mutators (print_file) use.
func stageOpenFile(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	file, _ := getString(in, "file")
	if strings.TrimSpace(file) == "" {
		return nil, fmt.Errorf("open_file: 'file' is required")
	}
	// A leading dash would be read as an option by `open` (and by the mdimport/
	// plutil probes); reject it rather than rely on positional consumption.
	if strings.HasPrefix(file, "-") {
		return nil, fmt.Errorf("open_file: path %q begins with '-' and is not allowed; prefix it with ./", file)
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, fmt.Errorf("open_file: cannot read %q: %w", file, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("open_file: %q is a directory, not a file", file)
	}

	// No app named → open with the system default handler.
	appRaw, _ := getString(in, "app")
	if strings.TrimSpace(appRaw) == "" {
		return &StagedPlan{
			Preview: fmt.Sprintf("Open %s with its default application. This cannot be undone automatically.", file),
			Forward: openFileForward("", file),
			Inverse: nil,
		}, nil
	}

	app, err := validateAppNameValue("open_file", "app", appRaw)
	if err != nil {
		return nil, err
	}

	plan := &StagedPlan{Forward: openFileForward(app, file)}
	// Reversibility (identical rationale to stageOpenApplication): only offer an
	// undo that quits the app when staging observed it was NOT already running.
	running := appAlreadyRunning(ctx, app)
	openClause := openUndoClause(app, running)
	if !running {
		inverse := osascriptCommand(quitScript, app)
		plan.Inverse = &inverse
	}

	// Fold the support verdict into the preview the user will confirm.
	plan.Preview = composeOpenFilePreview(file, app, openClause, assessFileSupport(ctx, app, file))
	return plan, nil
}

// openFileForward builds the `open` command for opening a file. With app empty it
// is `open -- <file>` (the default-handler form); otherwise `open -a <app> --
// <file>`. The "--" always precedes the file so `open` can never read the path as
// one of its own options. It is a pure helper (no probing) so the argv layout —
// specifically that the path always lands AFTER the terminator as data — is
// unit-testable directly.
func openFileForward(app, file string) Command {
	if app == "" {
		return Command{Binary: "open", Args: []string{"--", file}}
	}
	return Command{Binary: "open", Args: []string{"-a", app, "--", file}}
}

// openUndoClause renders the trailing sentence of an open_file preview describing
// what opening (and undo) will do, branching on whether the app is already running.
func openUndoClause(app string, running bool) string {
	if running {
		return fmt.Sprintf("%q is already running, so it will simply be brought forward; this cannot be undone.", app)
	}
	return fmt.Sprintf("If %q is not already running it will be launched; undo will quit it again.", app)
}

// composeOpenFilePreview builds the human-readable preview for an open_file stage,
// leading with any support warning so the user sees it before deciding. The
// supported case has no warning and reads as a plain intent sentence.
func composeOpenFilePreview(file, app, openClause string, s fileSupport) string {
	intent := fmt.Sprintf("Open %s in %q. %s", file, app, openClause)
	name := filepath.Base(file)
	switch s.Level {
	case supportUnsupported:
		warn := fmt.Sprintf("⚠️ %q does not appear to support %q (%s); it opens",
			app, name, s.FileType)
		if len(s.Accepts) > 0 {
			warn += " e.g. " + strings.Join(s.Accepts, ", ")
		} else {
			warn += " other types"
		}
		warn += ". Opening it may fail or show an error."
		return warn + "\n\n" + intent
	case supportUncertain:
		return fmt.Sprintf("⚠️ Could not confirm %q supports %q (%s); it may be unsupported.\n\n%s",
			app, name, s.Reason, intent)
	default: // supportSupported
		return intent
	}
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
//
// It reads the conventional "name" parameter; capabilities that carry the app
// under a different key (open_file uses "app") call validateAppNameValue directly.
func validateAppName(op string, in map[string]any) (string, error) {
	name, _ := getString(in, "name")
	return validateAppNameValue(op, "name", name)
}

// validateAppNameValue is the field-agnostic core of validateAppName: it applies
// the same non-empty / no-leading-dash / no-control-character checks to an already
// extracted value, naming field in the "required" error so the message matches the
// parameter the caller actually exposes.
func validateAppNameValue(op, field, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%s: '%s' is required", op, field)
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
