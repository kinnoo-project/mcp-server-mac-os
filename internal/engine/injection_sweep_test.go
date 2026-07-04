// injection_sweep_test.go is the centerpiece of the production security gate
// (see docs/TESTS.md). CLAUDE.md §4 mandates that every capability feeding a
// model-controlled value to a binary must neutralize option/code injection, and
// that each such capability ship a regression test. A per-capability convention
// is easy to forget when a new operation is added, so this file turns the
// convention into a registry-driven INVARIANT:
//
//  1. The structural guarantees that protect EVERY capability — the generic
//     builder's "--" terminator and the osascript seam's "--" terminator — are
//     hammered with a battery of hostile values, proving a flag-like or
//     shell-metacharacter value always lands as inert data.
//  2. A coverage gate enumerates every capability that takes a free-text
//     parameter and is answered by an in-process builtin (the cases the two
//     structural guarantees above do NOT automatically cover, because a builtin
//     assembles its own command), and fails the build if any such capability is
//     not on a reviewed allowlist that records HOW its injection guard works.
//
// Together these mean a newly-added capability with a free-text parameter cannot
// merge without either inheriting a proven structural guard or being explicitly
// reviewed and pointed at its regression test.
package engine

import (
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// hostileValues is the shared battery of adversarial parameter values. It mixes
// the two distinct hazards: leading-dash strings that a binary might parse as
// its OWN flags (option injection), and shell metacharacters that would matter
// IF a shell were ever involved (it never is — every command is exec'd with an
// explicit argv). A value from this list must always survive as a single,
// verbatim argv element in a position the target cannot interpret as an option.
var hostileValues = []string{
	"-e",          // osascript's own "execute this script" flag
	"-rf",         // rm-style destructive flag cluster
	"--flood",     // ping's flood flag
	"--",          // an end-of-options terminator supplied as data
	"-",           // a bare dash
	"; rm -rf /",  // shell command chaining (inert without a shell)
	"$(reboot)",   // shell command substitution (inert without a shell)
	"`reboot`",    // backtick command substitution (inert without a shell)
	"a && b",      // shell AND chaining
	"x\ny",        // embedded newline
	"x\x00y",      // embedded NUL
	"--include=*", // a valued flag in attached form
}

// reviewedFreeTextBuiltins records every capability that (a) accepts at least
// one free-text parameter AND (b) is answered by an in-process builtin rather
// than the generic argv builder or a mutator. Because a builtin builds its own
// command, it does NOT automatically inherit the generic builder's "--"
// terminator, so each one must carry its own injection guard. The value
// documents that guard and points at the dedicated regression test.
//
// TestInjection_BuiltinFreeTextParamsAreReviewed fails if the registry contains
// a free-text builtin missing from this map — that is the signal to add a guard
// and a regression test, then list it here.
var reviewedFreeTextBuiltins = map[string]string{
	"largest_files":       "in-process filepath.WalkDir; dir is used with the standard library, never passed to any binary",
	"image_info":          "sips: path validated by validateExistingOperand (rejects dash-leading, resolves absolute) before argv; sips has NO '--' terminator, so the absolute-path resolution is the guard; see media_filesystem_test.go",
	"spotlight_search":    "mdfind: dash-leading query rejected (mdfind has no '--'); optional scope dir resolved to an absolute path (leading '/') before -onlyin; see builtins_spotlight_test.go",
	"capture_screen":      "screencapture: output_path rejected if dash-leading and only ever used to CREATE a file (never overwrite); see builtins_screenshot_test.go",
	"capture_region":      "screencapture: output_path rejected if dash-leading (shared resolveScreenshotPath) and only ever used to CREATE a file; region is four validated ints, never free text; see builtins_screenshot_region_test.go",
	"capture_window":      "screencapture + osascript: app validated by validateAppNameValue then passed as argv data after '--' (probeWindowGeometry); output_path rejected if dash-leading; see builtins_screenshot_region_test.go",
	"list_applications":   "mdfind: dash-leading query rejected (mdfind has no '--'); see builtins_apps_test.go",
	"search_applications": "mdfind: dash-leading query rejected; see builtins_apps_test.go",
	"search_app_store":    "outbound HTTPS (no shell/argv): query carried only as the url.Values 'term' parameter (percent-encoded); scheme/host/path are Go-side constants, so a hostile value can only land as an encoded query value; see builtins_appstore_test.go",
	"list_windows":        "osascript via runOsascript: app filter passed as argv data after '--'; dash-leading/control-char rejected by validateAppNameValue; see builtins_windowing_test.go",
	"query_events":        "dates parsed via time.Parse; calendar name passed as osascript argv data after '--'; see builtins_calendar_test.go",
	"search_mail":         "mdfind: dash-leading query rejected (no '--' terminator); see builtins_mail_test.go",
	"list_inbox":          "osascript via runOsascript: mailbox passed as argv data after '--'; see builtins_mail_reads_test.go",
	"read_message":        "osascript via runOsascript: id validated numeric then passed as argv data after '--'; see builtins_mail_reads_test.go",
	"search_messages":     "sqlite3: term embedded via escapeSQLLiteral (quotes doubled, NUL rejected); see builtins_messages_test.go and builtins_messages_sqlinjection_test.go",
	"read_conversation":   "sqlite3: email validated then escaped, phone reduced to digits; see builtins_messages_test.go",
	"list_notes":          "osascript via runOsascript: folder passed as argv data after '--'; see builtins_notes_test.go",
	"search_notes":        "osascript via runOsascript: query/folder passed as argv data after '--'; see builtins_notes_test.go",
	"read_note":           "osascript via runOsascript: id passed as argv data after '--'; see builtins_notes_test.go",
	"find_contact":        "in-process Contacts lookup via osascript argv data after '--'; see builtins_phone_test.go",
	"get_contact":         "in-process Contacts lookup via osascript argv data after '--'; name query is inert data; see builtins_contacts_test.go",
	"ping_host":           "validateNetworkHost rejects dash-leading and metacharacters (ping/dig have no usable '--'); see builtins_network_test.go",
	"dns_lookup":          "validateNetworkHost rejects dash-leading and metacharacters; see builtins_network_test.go",
	"trace_route":         "validateNetworkHost rejects dash-leading and metacharacters (traceroute has no '--'); max_hops is a clamped int; see builtins_network_test.go",
	"whois_lookup":        "validateNetworkHost rejects dash-leading and metacharacters (whois has no '--', would read -h as a server redirect); see builtins_network_test.go",
	"dns_cache_lookup":    "validateNetworkHost rejects dash-leading and metacharacters (dscacheutil has no '--'); see builtins_network_test.go",
	"list_processes":      "ps with a fixed argv; filter applied in-process as a substring, never passed to the binary; see builtins_process_test.go",
	"system_log":          "log show: process/subsystem filters validated (no quote/backslash/control/dash) then composed Go-side into a `field == \"value\"` predicate whose quotes the value cannot escape; the raw string never reaches argv as an operand; see builtins_diagnostics_test.go",
	"list_reminders":      "in-process Reminders read; list name passed as osascript argv data after '--'; see builtins_reminders.go",
	"search_photos":       "osascript via runOsascript: query passed as argv data after '--' (Photos' own search); see builtins_photos_test.go",
	"get_photo":           "osascript via runOsascript: id passed as argv data after '--'; see builtins_photos_test.go",
	"get_album_photos":    "osascript via runOsascript: album name passed as argv data after '--'; see builtins_photos_test.go",
	"export_photo":        "osascript via runOsascript: id/destination passed as argv data after '--'; dash-leading destination rejected; exports only into a fresh empty dir (never overwrites); see builtins_photos_export_test.go",
	"verify_signature":    "codesign: path validated by validateExistingOperand (rejects dash-leading, resolves absolute) before argv; codesign has NO '--' terminator, so the absolute-path resolution is the guard; verb pinned to --verify (never sign); see builtins_security_test.go",
	"gatekeeper_check":    "spctl: path validated by validateExistingOperand (dash-rejected, absolute) before argv; spctl has no '--'; verb pinned to --assess (never --add/--enable/--master-disable); see builtins_security_test.go",
	"quarantine_info":     "xattr: path via validateExistingOperand (dash-rejected, absolute) AND placed after a '--' terminator (xattr honours it); verb pinned to -p (print, never -w/-d/-c); see builtins_security_test.go",
}

// hasFreeTextParam reports whether a capability takes any parameter whose value
// is model-controlled free text (string or path, scalar or list). Enums and
// ints are excluded: the validator already constrains an enum to its allowlist
// and an int to a whole number, so neither can carry an injection payload.
func hasFreeTextParam(c registry.Capability) bool {
	for _, p := range c.Params {
		switch p.Type {
		case registry.TypeString, registry.TypePath, registry.TypeStringList, registry.TypePathList:
			return true
		}
	}
	return false
}

// reviewedFreeTextMutators is the mutating-side twin of
// reviewedFreeTextBuiltins: every capability that (a) accepts at least one
// free-text parameter AND (b) is answered by a Mutator. A mutator assembles its
// own Forward/Inverse commands exactly like a builtin assembles its own argv,
// so it does NOT automatically inherit the generic builder's "--" terminator —
// each one must carry (or share) its own injection guard. The value documents
// that guard and points at the regression test proving it.
//
// TestInjection_MutatorFreeTextParamsAreReviewed fails if the registry contains
// a free-text mutator missing from this map — the signal to add a guard and a
// regression test before the capability can merge.
var reviewedFreeTextMutators = map[string]string{
	// Filesystem: every operand sits after a "--" terminator in mkdir/rmdir/mv/
	// cp/tee argv, and dash-leading paths are ALSO rejected up front as a
	// deliberate guardrail; tar has no positional operands (archive/dest are -f/
	// -C flag values) and its inputs carry the same dash-guard. See
	// mutate_filesystem_test.go and mutate_test.go.
	"mkdir":          "mkdir/rmdir with '--' before the path; dash-leading path rejected; see mutate_test.go",
	"move":           "mv with '--' before all operands; dash-leading source/dest/glob rejected; paths resolved absolute; see mutate_filesystem_test.go",
	"copy":           "cp -R with '--' before operands; dash-leading rejected; destination proven absent at stage; see mutate_filesystem_test.go",
	"remove":         "mv to ~/.Trash with '--' before operands; dash-leading rejected; see mutate_filesystem_test.go",
	"write_file":     "tee with '--' before the path, content via Stdin (never argv); dash-leading path rejected; see mutate_filesystem_test.go",
	"append_to_file": "tee -a with '--' before the path, content via Stdin; dash-leading path rejected; see mutate_filesystem_test.go",
	"compress":       "tar -c with '--' before member operands; dash-leading archive/source rejected; see mutate_filesystem_test.go",
	"extract":        "tar -x: archive/dest are -f/-C flag values (no positionals); dash-leading rejected; bsdtar's zip-slip defaults kept; see mutate_filesystem_test.go",

	// Media & document conversion: sips has NO '--' terminator, so every path is
	// dash-rejected AND resolved absolute (so it starts with '/', never a flag)
	// before argv; textutil/qlmanage also honour '--' before the source as
	// defense in depth; the 'format' is a registry-validated enum. Destinations
	// are proven absent at stage time; inverse mv-to-Trash. See media_filesystem_test.go.
	"convert_image":       "sips: source via validateExistingOperand + destination via validateNewOutputPath (both dash-rejected, absolute); format is an enum; sips has no '--'; inverse mv to Trash; see media_filesystem_test.go",
	"resize_image":        "sips: source/destination dash-rejected + absolute; dimension is a bounded int; format-free; sips has no '--'; inverse mv to Trash; see media_filesystem_test.go",
	"convert_document":    "textutil -convert with '--' before the source; source/destination dash-rejected + absolute; format is an enum; inverse mv to Trash; see media_filesystem_test.go",
	"quicklook_thumbnail": "qlmanage -t with '--' before the path; path via validateExistingOperand (dash-rejected, absolute); output dir is server-created scratch; size is a clamped int; inverse mv to Trash; see media_filesystem_test.go",

	// Clipboard / speech / notification: payload travels via Stdin or after a
	// "--" terminator or as osascript argv data — never as a bare operand.
	"write_clipboard": "pbcopy with EMPTY argv; text travels via Stdin so it can never be parsed as a flag; see mutate_clipboard_test.go",
	"notify":          "osascript via osascriptCommand: message/title passed as argv data after '--'; see mutate_system_test.go",
	"speak":           "say with '--' before the text operand ('-v Alex' lands as speech, not a voice flag); see mutate_system_test.go",

	// App / file / URL opening: app names go through validateAppNameValue
	// (non-empty, no leading dash, no control chars); file paths are dash-guarded
	// and placed after 'open --'; URLs are rebuilt from validated parts so a
	// hostile value can never reach argv verbatim.
	"open_application":  "open -a <name> with name via validateAppName (rejects dash-leading/control chars); inverse osascript argv data after '--'; see mutate_apps_test.go",
	"focus_application": "osascript via osascriptCommand: name via validateAppName then argv data after '--'; see mutate_apps_test.go",
	"quit_application":  "osascript via osascriptCommand: name via validateAppName then argv data after '--'; see mutate_apps_test.go",
	"open_file":         "open with '--' before the path; dash-leading path rejected; optional app via validateAppNameValue; see mutate_apps_test.go",
	"open_website":      "normalizeWebsiteURL rebuilds the URL (scheme allowlist, userinfo rejected, dash-leading rejected) before 'open'; browser via validateAppNameValue; see mutate_apps_test.go",
	"call":              "canonicalizePhoneNumber reduces input to digits/+ then callURL builds a fixed tel:/facetime: URL — free text never reaches argv; see mutate_phone_test.go",

	// Printing: lp places the file after a "--" terminator; file and printer
	// names are dash-guarded; copies is a bounded int.
	"print_file":      "lp with '--' before the file; dash-leading file/printer rejected; see mutate_printers_test.go",
	"print_test_page": "lp prints a server-written scratch file; only the printer name is model-controlled and it is dash-guarded; see mutate_printers_test.go",

	// AppleScript-backed application domains: every one of these goes through
	// the shared osascriptCommand seam, so all model values are argv data after
	// the structural '--' (TestInjection_OsascriptTerminatesHostileData).
	"send_mail":         "osascript argv data after '--'; see mutate_mail_test.go (hostile subject '-e' regression)",
	"send_message":      "osascript argv data after '--'; see mutate_messages_test.go",
	"create_contact":    "osascript argv data after '--'; delete-marker is crypto-random server-side; see mutate_contacts_test.go",
	"add_event":         "osascript argv data after '--'; dates parsed via time.Parse first; see mutate_calendar_test.go",
	"modify_event":      "osascript argv data after '--'; see mutate_calendar_test.go",
	"delete_event":      "osascript argv data after '--'; see mutate_calendar_test.go",
	"add_reminder":      "osascript argv data after '--'; see mutate_reminders_test.go",
	"modify_reminder":   "osascript argv data after '--'; see mutate_reminders_test.go",
	"complete_reminder": "osascript argv data after '--'; see mutate_reminders_test.go",
	"delete_reminder":   "osascript argv data after '--'; see mutate_reminders_test.go",
	"create_note":       "osascript argv data after '--'; body HTML-escaped server-side; see mutate_notes_test.go",
	"append_to_note":    "osascript argv data after '--'; see mutate_notes_test.go",
	"set_favorite":      "osascript argv data after '--'; see mutate_photos_test.go",
	"set_title":         "osascript argv data after '--'; see mutate_photos_test.go",
	"set_description":   "osascript argv data after '--'; see mutate_photos_test.go",
	"set_date":          "osascript argv data after '--'; date parsed into validated ints first; see mutate_photos_test.go",
	"set_keywords":      "osascript argv data after '--'; see mutate_photos_test.go",
	"create_album":      "osascript argv data after '--'; see mutate_photos_weak_test.go",
	"create_folder":     "osascript argv data after '--'; see mutate_photos_weak_test.go",
	"add_to_album":      "osascript argv data after '--'; see mutate_photos_weak_test.go",
	"import_photos":     "osascript argv data after '--'; file paths validated to exist first; see mutate_photos_weak_test.go",

	// Window management: the app name goes through validateAppNameValue and then
	// rides the osascript '--' seam; geometry values are validated ints.
	"move_window":     "osascript argv data after '--'; app via validateAppNameValue; coords are ints; see mutate_windowing_test.go",
	"resize_window":   "osascript argv data after '--'; app via validateAppNameValue; size is ints; see mutate_windowing_test.go",
	"minimize_window": "osascript argv data after '--'; app via validateAppNameValue; see mutate_windowing_test.go",
}

// TestInjection_BuiltinFreeTextParamsAreReviewed is the coverage gate: it walks
// the shipped registry and asserts that every free-text builtin capability is
// accounted for in reviewedFreeTextBuiltins, and that the allowlist has no stale
// entries. This is what makes "every model-controlled input is injection-safe"
// an enforced property of the whole catalog rather than a per-capability promise.
func TestInjection_BuiltinFreeTextParamsAreReviewed(t *testing.T) {
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}

	found := map[string]bool{}
	for _, c := range reg.All() {
		if _, isBuiltin := builtins[c.Builder]; !isBuiltin {
			continue // covered structurally by the generic builder or a mutator
		}
		if !hasFreeTextParam(c) {
			continue // no model-controlled free text to defend
		}
		found[c.Name] = true
		if _, reviewed := reviewedFreeTextBuiltins[c.Name]; !reviewed {
			t.Errorf("builtin %q takes a free-text parameter but has no entry in reviewedFreeTextBuiltins: add an injection guard + regression test, then document it here", c.Name)
		}
	}

	// Catch drift in the other direction: an allowlist entry that no longer
	// corresponds to a free-text builtin is misleading and should be removed.
	for name := range reviewedFreeTextBuiltins {
		if !found[name] {
			t.Errorf("reviewedFreeTextBuiltins lists %q, but it is no longer a free-text builtin capability — remove the stale entry", name)
		}
	}
}

// TestInjection_MutatorFreeTextParamsAreReviewed is the mutating-side coverage
// gate, the mirror image of TestInjection_BuiltinFreeTextParamsAreReviewed: it
// walks the shipped registry and asserts every free-text capability answered by
// a Mutator is accounted for in reviewedFreeTextMutators (and that the
// allowlist carries no stale entries). Without this gate a future free-text
// mutator could merge with neither a guard nor a regression test and nothing
// would fail.
func TestInjection_MutatorFreeTextParamsAreReviewed(t *testing.T) {
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}

	found := map[string]bool{}
	for _, c := range reg.All() {
		if _, isMutator := mutators[c.Builder]; !isMutator {
			continue // a builtin (gated above) or the generic/named builder (structural "--")
		}
		if !hasFreeTextParam(c) {
			continue // no model-controlled free text to defend
		}
		found[c.Name] = true
		if _, reviewed := reviewedFreeTextMutators[c.Name]; !reviewed {
			t.Errorf("mutator %q takes a free-text parameter but has no entry in reviewedFreeTextMutators: add an injection guard + regression test, then document it here", c.Name)
		}
	}

	for name := range reviewedFreeTextMutators {
		if !found[name] {
			t.Errorf("reviewedFreeTextMutators lists %q, but it is no longer a free-text mutator capability — remove the stale entry", name)
		}
	}
}

// TestInjection_GenericBuilderTerminatesHostilePositionals proves the structural
// guarantee that protects every generic-builder capability (ls, file, stat, wc,
// du, sort, head, ...): a positional operand, whatever it contains, is always
// emitted after the "--" terminator as one verbatim token, so it can never be
// parsed as a flag and is never split on a metacharacter.
func TestInjection_GenericBuilderTerminatesHostilePositionals(t *testing.T) {
	// A synthetic capability with a leading flag and a positional path operand,
	// mirroring the shape of a real read-only inspector.
	c := registry.Capability{
		Name: "sweep_generic",
		Params: []registry.ParamSpec{
			{Name: "long", Type: registry.TypeBool, Arg: registry.ArgRule{Kind: registry.ArgFlag, Flag: "-l"}},
			{Name: "target", Type: registry.TypePath, Arg: registry.ArgRule{Kind: registry.ArgPositional}},
		},
	}

	for _, h := range hostileValues {
		normalized, err := normalizeParams(c, map[string]any{"long": true, "target": h})
		if err != nil {
			t.Errorf("%q: normalizeParams failed: %v", h, err)
			continue
		}
		argv, err := buildGeneric(c, normalized)
		if err != nil {
			t.Errorf("%q: buildGeneric failed: %v", h, err)
			continue
		}
		// With exactly one positional, argv must end "... -- <value>", and the
		// value must be byte-for-byte what we passed (no splitting, no mangling).
		if len(argv) < 2 || argv[len(argv)-2] != "--" {
			t.Errorf("%q: expected a '--' terminator immediately before the positional, got argv %q", h, argv)
			continue
		}
		if got := argv[len(argv)-1]; got != h {
			t.Errorf("%q: positional landed as %q, want the value verbatim", h, got)
		}
	}
}

// TestInjection_OsascriptTerminatesHostileData proves the structural guarantee
// for every AppleScript-backed capability: osascriptCommand always inserts a
// "--" between the fixed script and the first data argument, so a value like
// "-e" reaches the script as data bound to `on run argv` rather than being
// parsed by osascript as another "-e <statement>" to execute.
func TestInjection_OsascriptTerminatesHostileData(t *testing.T) {
	const script = "on run argv\nreturn item 1 of argv\nend run"

	for _, h := range hostileValues {
		cmd := osascriptCommand(script, h)
		if cmd.Binary != "osascript" {
			t.Errorf("%q: binary = %q, want osascript", h, cmd.Binary)
			continue
		}
		// argv must be exactly: -e <script> -- <value>
		want := []string{"-e", script, "--", h}
		if len(cmd.Args) != len(want) {
			t.Errorf("%q: argv = %q, want %q", h, cmd.Args, want)
			continue
		}
		for i := range want {
			if cmd.Args[i] != want[i] {
				t.Errorf("%q: argv[%d] = %q, want %q (full argv %q)", h, i, cmd.Args[i], want[i], cmd.Args)
			}
		}
	}

	// Multiple data arguments: the terminator sits once, before ALL of them, so
	// each subsequent value is data regardless of a leading dash.
	cmd := osascriptCommand(script, "-e", "second", "--flood")
	termIdx := indexOf(cmd.Args, "--")
	if termIdx < 0 {
		t.Fatalf("multi-arg: no '--' terminator in argv %q", cmd.Args)
	}
	for _, data := range []string{"-e", "second", "--flood"} {
		if !appearsAfter(cmd.Args, data, termIdx) {
			t.Errorf("multi-arg: data %q does not appear after the terminator in argv %q", data, cmd.Args)
		}
	}
}

// indexOf returns the index of the first occurrence of v in s, or -1.
func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// appearsAfter reports whether v appears in s at some index strictly greater
// than after.
func appearsAfter(s []string, v string, after int) bool {
	for i := after + 1; i < len(s); i++ {
		if s[i] == v {
			return true
		}
	}
	return false
}
