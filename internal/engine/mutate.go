// mutate.go is the engine's mutation seam: the counterpart to the read-only Run
// pipeline for capabilities that CHANGE system state.
//
// # Why mutation is shaped differently from reads
//
// A read-only capability is a pure function of its parameters, so Run can
// validate, build argv, and execute in a single call. A mutation cannot: the
// only safe moment to show a human what will happen — and the only moment we can
// capture the prior state needed to reverse it — is AFTER validation but BEFORE
// execution. So mutation is split into two phases bridged by a token (see
// internal/transaction and .claude/rules/transactional-state.md):
//
//	Stage:  validate -> capture undo state -> assemble forward & inverse commands -> preview   (NO side effects)
//	commit: run the staged Forward command                                                     (the actual change)
//	undo:   run the staged Inverse command                                                     (the reversal)
//
// Crucially, BOTH the forward and inverse commands are fully resolved at stage
// time, so whatever prior state staging observed is baked into the inverse. The
// later commit/undo steps just run those pre-built commands and recompute
// nothing — which is what lets the server promise that "what executes is exactly
// what was staged and previewed."
//
// A "mutator" is the mutating analogue of the named argv builders in
// argbuild.go: declarative manifest data drives validation/risk/reversibility,
// while the inherently stateful logic (probing prior state, choosing the right
// inverse) lives in a small named Go function selected by Capability.Builder.
// Per-domain mutators live in their own files (mutate_filesystem.go,
// mutate_preferences.go, ...), mirroring how builders_filesystem.go separates
// the named argv builders from the generic machinery in argbuild.go. This file
// holds only the machinery shared by every mutator.
package engine

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// Command is a fully-tokenized, ready-to-run invocation: a bare binary name
// (resolved to a trusted absolute path through the policy layer at run time) and
// its explicit positional argument vector. It carries no shell string — argv is
// already split — which preserves the project's no-shell execution axiom across
// the stage/commit boundary.
type Command struct {
	// Binary is the bare utility name (e.g. "mkdir"); policy.ResolveBinary maps
	// it to a trusted absolute path before execution.
	Binary string
	// Args is the pre-split argument vector passed to the binary verbatim.
	Args []string
	// Stdin, when non-nil, is fed to the child's standard input. It is the
	// channel for content payloads (file bodies, clipboard text) that must reach
	// a utility like tee or pbcopy as pure data: bytes on stdin can never be
	// parsed as argv flags or paths, so they need no dash-guard/`--` hardening.
	// That guarantee is about argv only — an interpreter (sh, python, osascript)
	// would happily execute its stdin as code, so a mutator must pair Stdin with
	// a data-sink utility (tee, pbcopy), never an interpreter. Payloads are
	// capped at maxStdinBytes by RunCommand. Because staged plans (and their
	// inverses) are stored and replayed as Command values, a payload captured at
	// stage time — including prior state baked into an inverse — flows through
	// commit and undo untouched.
	Stdin []byte
	// DiscardStdout drops the child's stdout from the rendered result (stderr
	// and the exit code still surface). Data-sink utilities like tee echo every
	// byte they write; that echo is redundant for a forward command (the caller
	// supplied the content) and an outright leak for an inverse baked from a
	// file's PRIOR contents — undo would otherwise return data to the client
	// that it never read through any approved operation.
	DiscardStdout bool
	// Detach runs the command as a background process that OUTLIVES the tool
	// call, returning its PID immediately instead of waiting for it to exit. It
	// is set only by mutators whose effect is a long-lived helper the request
	// context must NOT cancel — today keep_awake's detached caffeinate, which
	// must keep running for the requested duration after the commit call returns.
	// The engine routes such a Command through execDetached (see RunCommand); a
	// detached Command carries no Stdin.
	Detach bool
}

// StagedPlan is the product of staging a mutating capability: everything needed
// to commit the change and, when possible, to reverse it, plus human-readable
// text describing the effect.
type StagedPlan struct {
	// Preview is a plain-language description of what committing will do and what
	// undo will do, suitable for showing a user before they approve.
	Preview string
	// Forward is the command that performs the mutation when committed.
	Forward Command
	// Inverse is the compensating command that reverses the mutation, or nil when
	// the operation is irreversible (in which case there is no undo to offer).
	Inverse *Command
}

// maxStdinBytes caps a Command's stdin payload at the RunCommand choke point.
// Staged plans (and their inverses, which may bake in prior file contents) sit
// in the in-memory transaction stores until consumed or expired, so an
// unbounded payload would be a memory-pressure/DoS vector — the same class of
// risk the stdout compaction and pipeline intermediate caps already guard.
// Individual mutators impose their own tighter, purpose-fit limits (e.g. 1 MiB
// for a new file body); this ceiling is the engine-wide backstop, sized so a
// legitimate inverse (prior contents of a several-MiB file) still fits. A var
// (not a const) so tests can lower it.
var maxStdinBytes = 16 << 20 // 16 MiB

// Mutator stages one mutating capability. Like an ArgBuilder it receives the
// already-normalized parameter map, but instead of returning argv it performs
// any read-only probing required to capture prior state and returns a complete
// StagedPlan. A Mutator MUST NOT change system state — that is the commit step's
// job.
type Mutator func(ctx context.Context, c registry.Capability, in map[string]any) (*StagedPlan, error)

// mutators maps a capability's Builder name to its mutator implementation. It is
// disjoint from the read-only `builders` and `builtins` maps: a capability is
// read-only (run via Run) or mutating (staged via Stage), never both.
var mutators = map[string]Mutator{
	"mkdir":               stageMkdir,
	"move":                stageMove,
	"copy":                stageCopy,
	"remove":              stageRemove,
	"write_file":          stageWriteFile,
	"append_to_file":      stageAppendToFile,
	"compress":            stageCompress,
	"extract":             stageExtract,
	"convert_image":       stageConvertImage,
	"resize_image":        stageResizeImage,
	"convert_document":    stageConvertDocument,
	"quicklook_thumbnail": stageQuicklookThumbnail,
	"write_clipboard":     stageWriteClipboard,
	"write_setting":       stageWriteSetting,
	"set_appearance":      stageSetAppearance,
	"send_mail":           stageSendMail,
	"add_event":           stageAddEvent,
	"modify_event":        stageModifyEvent,
	"delete_event":        stageDeleteEvent,
	"add_reminder":        stageAddReminder,
	"modify_reminder":     stageModifyReminder,
	"complete_reminder":   stageCompleteReminder,
	"delete_reminder":     stageDeleteReminder,
	"call":                stageCall,
	"create_contact":      stageCreateContact,
	"send_message":        stageSendMessage,
	"create_note":         stageCreateNote,
	"append_to_note":      stageAppendToNote,
	"set_favorite":        stageSetFavorite,
	"set_title":           stageSetTitle,
	"set_description":     stageSetDescription,
	"set_date":            stageSetDate,
	"set_keywords":        stageSetKeywords,
	"create_album":        stageCreateAlbum,
	"create_folder":       stageCreateFolder,
	"add_to_album":        stageAddToAlbum,
	"import_photos":       stageImportPhotos,
	"open_application":    stageOpenApplication,
	"open_file":           stageOpenFile,
	"open_website":        stageOpenWebsite,
	"open_app_store_page": stageOpenAppStorePage,
	"focus_application":   stageFocusApplication,
	"quit_application":    stageQuitApplication,
	"play_pause":          stagePlayPause,
	"next_track":          stageNextTrack,
	"previous_track":      stagePreviousTrack,
	"move_window":         stageMoveWindow,
	"resize_window":       stageResizeWindow,
	"minimize_window":     stageMinimizeWindow,
	"print_file":          stagePrintFile,
	"print_test_page":     stagePrintTestPage,
	"open_settings":       stageOpenSettings,
	"notify":              stageNotify,
	"speak":               stageSpeak,
	"display_sleep":       stageDisplaySleep,
	"wifi_set_power":      stageWifiSetPower,
	"keep_awake":          stageKeepAwake,
	"allow_sleep":         stageAllowSleep,
	"quit_process":        stageQuitProcess,
	"terminate_process":   stageTerminateProcess,
	"flush_dns_cache":     stageFlushDNSCache,
	"mount_volume":        stageMountVolume,
	"attach_disk_image":   stageAttachDiskImage,
	"detach_disk_image":   stageDetachDiskImage,
}

// lookupMutator returns the mutator for a builder name and whether one exists.
func lookupMutator(name string) (Mutator, bool) {
	m, ok := mutators[name]
	return m, ok
}

// Stage validates a mutating capability's parameters and produces a StagedPlan
// WITHOUT performing the mutation. It is the mutating counterpart to Run;
// callers must route read-only capabilities through Run and mutating ones
// through Stage (the server decides based on Capability.Reversibility).
//
// As with Run, ctx is honoured up front so a cancelled request stages nothing,
// and parameters are validated/normalized before the mutator ever sees them.
func (e *Engine) Stage(ctx context.Context, c registry.Capability, raw map[string]any) (*StagedPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := normalizeParams(c, raw)
	if err != nil {
		return nil, err
	}
	mutate, ok := lookupMutator(c.Builder)
	if !ok {
		return nil, fmt.Errorf("engine: capability %q references unknown mutator %q", c.Name, c.Builder)
	}
	return mutate(ctx, c, normalized)
}

// RunCommand resolves and executes a single pre-built Command, returning its
// rendered output. It is the shared execution primitive behind both committing a
// staged plan (its Forward command) and undoing one (its Inverse command).
//
// Because the Command was assembled at stage time from validated input, this
// path performs no further parsing — it only enforces the same policy trust
// check and context binding that every subprocess in the engine obeys. Mutators
// themselves are plain functions (not Engine methods) and use the package-level
// runCommand/policy.ResolveBinary directly for any read-only probing they need
// before a plan is assembled (see mutate_preferences.go).
func (e *Engine) RunCommand(ctx context.Context, cmd Command) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(cmd.Stdin) > maxStdinBytes {
		return "", fmt.Errorf("engine: stdin payload is %d bytes, exceeding the %d-byte limit", len(cmd.Stdin), maxStdinBytes)
	}
	bin, err := policy.ResolveBinary(cmd.Binary)
	if err != nil {
		return "", err
	}
	// A detached command (keep_awake's caffeinate) must outlive this call, so it
	// takes the fire-and-return path that starts the child and reports its PID
	// rather than waiting for it under the request context. The detach path does
	// not wire stdin (execDetached never reads it), so a payload here would be
	// silently lost — reject it explicitly rather than let a future mutator
	// attach one unnoticed. This enforces the "detached carries no stdin"
	// contract documented on Command.Detach.
	if cmd.Detach {
		if len(cmd.Stdin) > 0 {
			return "", fmt.Errorf("engine: a detached command must not carry stdin (%d bytes supplied)", len(cmd.Stdin))
		}
		res, err := execDetached(ctx, bin, cmd.Args...)
		if err != nil {
			return "", err
		}
		return formatRunResult(res), nil
	}
	res, err := execCommand(ctx, bin, cmd.Stdin, cmd.Args...)
	if err != nil {
		return "", err
	}
	if cmd.DiscardStdout {
		res.Stdout = ""
	}
	return formatRunResult(res), nil
}

// previewSnippet renders a short, human-safe description of a content payload
// for a staged-plan preview: the total byte count plus the (truncated, quoted)
// first line. Previews are shown to a person before they approve a mutation, so
// they must convey what will be written without dumping a potentially huge or
// control-character-laden payload into the confirmation text — %q-style quoting
// escapes anything that could mangle a terminal or impersonate preview markup.
func previewSnippet(content []byte) string {
	if len(content) == 0 {
		return "empty (0 bytes)"
	}
	first := content
	if i := bytes.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	// elided marks that the snippet shows less than the full payload, either
	// because more lines follow or because the first line itself was cut.
	elided := len(first) < len(content)
	const maxSnippetBytes = 80
	if len(first) > maxSnippetBytes {
		first = first[:maxSnippetBytes]
		elided = true
	}
	quoted := strconv.Quote(strings.TrimSuffix(string(first), "\r"))
	if elided {
		return fmt.Sprintf("%d bytes, beginning %s …", len(content), quoted)
	}
	return fmt.Sprintf("%d bytes: %s", len(content), quoted)
}
