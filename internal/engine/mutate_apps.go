// mutate_apps.go implements the mutating Application operations: launching,
// focusing, and quitting an app, opening a file, and opening a website.
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
	"net/url"
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
			Preview: fmt.Sprintf("Open file %s with its default application. This can't be undone automatically. Proceed?", file),
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
	undoNote := openUndoNote(running)
	if !running {
		inverse := osascriptCommand(quitScript, app)
		plan.Inverse = &inverse
	}

	// Fold the support verdict into the preview the user will confirm.
	plan.Preview = composeOpenFilePreview(file, app, undoNote, assessFileSupport(ctx, app, file))
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

// openUndoNote renders the short sentence describing what launching (and undo)
// will do. Staging already knows the running state, so the phrasing is definite
// rather than conditional.
func openUndoNote(running bool) string {
	if running {
		return "It is already running, so it will just be brought to the front; this can't be undone."
	}
	return "It will be launched; undo will quit it again."
}

// composeOpenFilePreview builds the human-readable preview for an open_file stage.
// It leads with a clear, concrete intent sentence — "Open file <path> with
// "<app>"." — followed by the undo note and a "Proceed?" call to action, so the
// confirmation reads naturally. When support is in doubt, a ⚠️ warning is placed
// FIRST (on its own line) so the user sees the risk before the action.
func composeOpenFilePreview(file, app, undoNote string, s fileSupport) string {
	intent := fmt.Sprintf("Open file %s with %q.", file, app)
	tail := strings.TrimSpace(strings.TrimSpace(undoNote) + " Proceed?")
	action := strings.TrimSpace(intent + " " + tail)
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
		return warn + "\n\n" + action
	case supportUncertain:
		return fmt.Sprintf("⚠️ Could not confirm %q supports %q (%s); it may be unsupported.\n\n%s",
			app, name, s.Reason, action)
	default: // supportSupported
		return action
	}
}

// stageOpenWebsite stages opening a web address in a browser — the third member
// of the "open …" family alongside open_application (installed apps) and
// open_file (files on disk). Like open_file it is NOT auto-commit: it waits behind
// the execute confirmation gate, and it offers no undo (closing exactly the tab
// that was opened is not something `open` can reliably reverse).
//
// # Why the URL is validated, not trusted
//
// `open` will launch WHATEVER scheme it is handed — `file://…` reads a local file,
// `tel:`/`facetime:` place a call, a custom scheme triggers its registered handler.
// So a model-supplied address cannot be passed through verbatim. normalizeWebsiteURL
// constrains it to http/https (rejecting every other scheme) and upgrades a bare
// domain like "youtube.com" to "https://youtube.com" — the same "the scheme is
// chosen by this code, never by the model" discipline that mutate_phone.go and
// mutate_system.go apply to their URLs.
//
// The forward command places the (now guaranteed http/https) URL after a "--"
// terminator so `open` can never read it as one of its own options, mirroring
// openFileForward.
func stageOpenWebsite(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	raw, _ := getString(in, "url")
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("open_website: 'url' is required")
	}

	normalized, err := normalizeWebsiteURL(raw)
	if err != nil {
		return nil, err
	}

	// Optional browser: validated like any other app name (non-empty after trim,
	// no leading dash, no control characters). Omitted → system default browser.
	browserRaw, _ := getString(in, "browser")
	var browser string
	if strings.TrimSpace(browserRaw) != "" {
		browser, err = validateAppNameValue("open_website", "browser", browserRaw)
		if err != nil {
			return nil, err
		}
	}

	var (
		forward Command
		preview string
	)
	if browser == "" {
		forward = Command{Binary: "open", Args: []string{"--", normalized}}
		preview = fmt.Sprintf("Open %s in your default browser. Proceed?", normalized)
	} else {
		forward = Command{Binary: "open", Args: []string{"-a", browser, "--", normalized}}
		preview = fmt.Sprintf("Open %s in %q. Proceed?", normalized, browser)
	}

	return &StagedPlan{
		Preview: preview,
		Forward: forward,
		Inverse: nil, // no reliable way to close just the tab/window that opened
	}, nil
}

// nonWebSchemes are URL schemes a website-opening tool must never act on. They
// are listed so a value like "tel:911" or "mailto:x" yields a clear "only http
// and https" error rather than being misread as a host:port (see
// normalizeWebsiteURL). The set is not exhaustive — anything that is neither a
// recognised host:port nor an http(s) URL is rejected regardless — but naming the
// common phone/file/mail schemes makes the error message specific.
var nonWebSchemes = map[string]bool{
	"tel": true, "sms": true, "mailto": true, "facetime": true,
	"facetime-audio": true, "file": true, "ftp": true, "ftps": true,
	"javascript": true, "data": true, "smb": true, "afp": true,
	"vnc": true, "ssh": true,
}

// normalizeWebsiteURL turns a model-supplied address into a safe, fully-qualified
// http(s) URL or returns an error. It is pure (no I/O) so the normalization and
// every rejection can be unit-tested directly.
//
// Rules:
//   - A leading "-" is rejected up front: even though the forward command uses a
//     "--" terminator, an address should never look like an option, and this keeps
//     the guard obvious and local (matches open_file's path check).
//   - Control characters and whitespace are rejected — a real web address has
//     none, and they are exactly what a smuggled second argument would rely on.
//   - A full "scheme://…" value must be http or https with a host; every other
//     scheme (file://, ftp://, …) is rejected.
//   - A value with no "://" is treated as a bare domain or host:port and upgraded
//     to "https://<value>" — UNLESS it carries a non-web scheme prefix. The tricky
//     part is that url.Parse reports a "scheme" for both "tel:911" (a real scheme,
//     reject) and "localhost:8080" (a host:port, accept): they are distinguished
//     by the opaque part being an all-digit port and the scheme not being a known
//     non-web scheme. A bare "http:example.com" (a web scheme missing its "//") is
//     rejected as malformed rather than silently rewritten.
//
// The net guarantee is unchanged: whatever is returned is an http/https URL with a
// host, so `open` is only ever handed a web address — never a file:, tel:, or
// custom-scheme value it would dispatch to another handler.
func normalizeWebsiteURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("open_website: url is required")
	}
	if strings.HasPrefix(v, "-") {
		return "", fmt.Errorf("open_website: url %q must not begin with '-'", raw)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return "", fmt.Errorf("open_website: url must not contain spaces or control characters")
		}
	}

	// A full URL carries its scheme explicitly; validate it as-is.
	if strings.Contains(v, "://") {
		return validateWebURL(v, raw)
	}

	// No "://": the value is a bare domain ("youtube.com"), a bare host:port
	// ("localhost:8080"), or a "scheme:opaque" the model should not be sending
	// ("tel:911", "http:example.com"). url.Parse reports a Scheme only for the
	// "word:" shapes, which is how we tell a host:port apart from a real scheme.
	if u, err := url.Parse(v); err == nil && u.Scheme != "" {
		scheme := strings.ToLower(u.Scheme)
		switch {
		case scheme == "http" || scheme == "https":
			// e.g. "http:example.com" — a web scheme but missing its "//". Don't
			// silently rewrite it into a bogus URL; treat it as malformed input.
			return "", fmt.Errorf("open_website: %q is missing %q after the scheme; write it as %s://…", raw, "//", scheme)
		case nonWebSchemes[scheme]:
			return "", fmt.Errorf("open_website: only http and https web addresses are allowed, got scheme %q", u.Scheme)
		case isAllDigits(u.Opaque):
			// host:port — the "scheme" is really the host and the all-digit opaque
			// is the port (e.g. "localhost:8080", "example.com:8080"). Fall through
			// and default it to https below.
		default:
			// An unrecognised scheme with a non-port opaque: not a web address.
			return "", fmt.Errorf("open_website: only http and https web addresses are allowed, got scheme %q", u.Scheme)
		}
	}

	return validateWebURL("https://"+v, raw)
}

// validateWebURL parses an already-assembled candidate URL and confirms it is an
// http/https address with a host, returning the canonical form. raw is the
// caller's original input, used only for clearer error messages.
func validateWebURL(candidate, raw string) (string, error) {
	u, err := url.Parse(candidate)
	if err != nil {
		return "", fmt.Errorf("open_website: %q is not a valid URL: %w", raw, err)
	}
	if scheme := strings.ToLower(u.Scheme); scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("open_website: only http and https web addresses are allowed, got scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("open_website: %q has no host", raw)
	}
	// Reject embedded userinfo ("user:pass@host" / "name@host"). A real website to
	// open never needs it, and it is the classic phishing disguise — the host the
	// user thinks they're visiting ("trusted.com" in "trusted.com@evil.com") is
	// actually only userinfo, and the browser navigates to the host AFTER the "@"
	// ("evil.com"). Refusing it also avoids handing credentials to `open`.
	if u.User != nil {
		return "", fmt.Errorf("open_website: %q must not contain embedded credentials or an '@' before the host", raw)
	}
	return u.String(), nil
}

// isAllDigits reports whether s is non-empty and made up solely of ASCII digits —
// the test for whether a "scheme:opaque" value's opaque part is really a port
// (and the whole value therefore a host:port, not a custom scheme).
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// stageOpenAppStorePage stages (for immediate auto-commit) opening a specific
// app's page in the App Store app. It is the outbound half of the download flow:
// search_app_store finds the app and its numeric id, and this opens that app's
// store page so the user can click "Get"/"Buy" themselves — installing is never
// automated (no first-party CLI, and a purchase needs an Apple ID interaction).
//
// Like open_settings it just opens a window, so it is auto-commit and
// irreversible (there is nothing to undo). The forward URL is built entirely in
// Go from a validated NUMERIC id: track_id is an int parameter, so the normalizer
// has already constrained it to a whole number before we ever see it. There is
// therefore no free text in the URL and no injection surface — the value can only
// be digits, and the scheme/host/path are constants. This is why the op takes an
// int id rather than a name: it keeps the URL injection-inert (unlike open_website,
// whose free-form URL is why that op stays staged behind the execute gate).
func stageOpenAppStorePage(_ context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	id, ok := getInt(in, "track_id")
	if !ok {
		return nil, fmt.Errorf("open_app_store_page: 'track_id' is required (the numeric App Store id from search_app_store)")
	}
	if id <= 0 {
		return nil, fmt.Errorf("open_app_store_page: track_id must be a positive App Store id, got %d", id)
	}
	// Built Go-side from an integer: no model-controlled text reaches the URL.
	appStoreURL := fmt.Sprintf("macappstore://apps.apple.com/app/id%d", id)
	return &StagedPlan{
		Preview: fmt.Sprintf("Open the App Store to the app with id %d. Installing or buying it stays your click — this only opens its page.", id),
		Forward: Command{Binary: "open", Args: []string{appStoreURL}},
		Inverse: nil, // nothing to undo: it just opens a window
	}, nil
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
