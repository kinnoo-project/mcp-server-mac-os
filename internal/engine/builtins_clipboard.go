// builtins_clipboard.go implements read_clipboard: a read-only look at the text
// currently on the macOS clipboard (pasteboard).
//
// The clipboard is a property of the logged-in user's window session, not of
// this process, so — unlike pwd — the answer cannot come from the standard
// library; it must be read from the system pasteboard. macOS exposes that
// through the trusted /usr/bin/pbpaste utility, which prints the pasteboard's
// text to stdout. read_clipboard is a builtin (rather than a generic-builder
// capability) purely so it can bound and label its own output: builtin output
// bypasses the subprocess truncation budget, and the clipboard can hold a very
// large paste, so this file caps the returned text itself.
//
// This capability takes NO parameters, so there is no model-controlled value to
// harden and no injection surface — the pbpaste argv is fixed.
package engine

import (
	"context"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// runReadClipboard returns the plain-text contents of the macOS clipboard, or a
// clear "empty" note when there is no text to paste.
//
// It shells out to pbpaste with a fixed argument vector and bounds the result
// through the same head/tail compaction the subprocess layer applies to every
// other command, because a builtin's output is not otherwise capped. The text is
// returned verbatim: read_clipboard makes no attempt to redact secrets (the
// manifest summary warns the caller that the clipboard may hold a password), but
// it never logs or persists the value — it flows straight back as the tool
// result.
func runReadClipboard(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("pbpaste")
	if err != nil {
		return "", err
	}
	// pbpaste emits nothing extra and no trailing newline of its own; a fixed
	// argv means there is no user input to guard.
	out, err := runCommand(ctx, bin, "-Prefer", "txt")
	if err != nil {
		return "", err
	}
	if err := commandFailed("pbpaste", out); err != nil {
		return "", err
	}
	return formatClipboardText(out.Stdout), nil
}

// formatClipboardText is the pure rendering half of runReadClipboard: it turns
// pbpaste's raw stdout into the tool result. Empty output means the clipboard
// held no text (it is empty, or holds an image/file), which is reported as a
// clear note rather than a blank result; any text is returned through the same
// head/tail compaction the subprocess layer applies elsewhere, since a builtin's
// output is not otherwise capped. Splitting this out keeps the size-bounding and
// empty-clipboard behavior unit-testable without a live pbpaste call.
func formatClipboardText(stdout string) string {
	if stdout == "" {
		return "The clipboard is empty (or holds non-text content such as an image or file)."
	}
	return compactOutput(stdout)
}
