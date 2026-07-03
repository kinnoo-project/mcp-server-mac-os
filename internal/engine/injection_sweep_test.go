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
	"list_processes":      "ps with a fixed argv; filter applied in-process as a substring, never passed to the binary; see builtins_process_test.go",
	"list_reminders":      "in-process Reminders read; list name passed as osascript argv data after '--'; see builtins_reminders.go",
	"search_photos":       "osascript via runOsascript: query passed as argv data after '--' (Photos' own search); see builtins_photos_test.go",
	"get_photo":           "osascript via runOsascript: id passed as argv data after '--'; see builtins_photos_test.go",
	"get_album_photos":    "osascript via runOsascript: album name passed as argv data after '--'; see builtins_photos_test.go",
	"export_photo":        "osascript via runOsascript: id/destination passed as argv data after '--'; dash-leading destination rejected; exports only into a fresh empty dir (never overwrites); see builtins_photos_export_test.go",
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
