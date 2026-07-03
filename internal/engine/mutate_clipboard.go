// mutate_clipboard.go holds the clipboard domain's mutator: write_clipboard, the
// mutating counterpart to the read-only read_clipboard builtin.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// maxClipboardBytes caps both the text write_clipboard will place on the
// clipboard AND the prior contents it will hold for undo. The new text travels
// on the forward command's stdin (inert data, see Command.Stdin) and the prior
// text is baked into the inverse's stdin, so both sit in the staged/undo token
// stores until consumed — hence a bound well below the engine-wide backstop.
// 1 MiB comfortably covers any text a user would sensibly copy; a larger prior
// clipboard simply means undo is not offered (see below).
const maxClipboardBytes = 1 << 20 // 1 MiB

// stageWriteClipboard stages a reversible replacement of the clipboard's text.
//
// Forward is `pbcopy` with the new text on stdin — pbcopy is a pure data sink
// (stdin bytes become the clipboard verbatim and can never be parsed as flags or
// code), so the model-controlled text needs no argv hardening; there is no argv
// operand at all. Because the operation runs in the auto_commit lane it applies
// immediately, but it is still staged first so an inverse can be computed.
//
// Undo restores the clipboard's PRIOR text. Staging probes the current clipboard
// with a read-only pbpaste and bakes those bytes into an inverse `pbcopy`, giving
// a byte-exact restore. Two cases yield NO undo (Inverse == nil), which the
// preview states plainly:
//   - the prior clipboard was empty OR held non-text content (an image, a file):
//     pbpaste returns no text, so there is nothing to restore to — clearing the
//     clipboard would NOT bring an image back, so pretending to undo would be a
//     lie. We decline the undo rather than silently clear;
//   - the prior text exceeds maxClipboardBytes: holding it as an undo payload is
//     out of proportion to a clipboard write, so undo is declined.
func stageWriteClipboard(ctx context.Context, _ registry.Capability, in map[string]any) (*StagedPlan, error) {
	text, _ := getString(in, "text")
	// Reject empty (or whitespace-only) text rather than silently CLEARING the
	// clipboard: the required-check only catches an absent key, so `text: ""`
	// would otherwise pass and stage a clear the preview describes as a
	// "replace". Every comparable mutator (notify, speak, append_to_file)
	// rejects empty input the same way; a deliberate clear can be expressed by
	// writing a space or other explicit content.
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("write_clipboard: 'text' is empty; refusing to silently clear the clipboard")
	}

	// Probe the current clipboard text for the inverse. This is a read-only
	// operation (no state change), exactly what a mutator is permitted to do at
	// stage time. A fixed pbpaste argv means there is no input to guard here.
	//
	// We read pbpaste's full stdout and then bound it in planWriteClipboard
	// (declining undo when it exceeds maxClipboardBytes), rather than streaming a
	// capped read. This is the same buffer-then-check shape the pipeline's
	// intermediate-size cap uses (pipeline.go), and it keeps this probe on the
	// shared, context-bound execCommand path that every other read in the engine
	// uses (including read_clipboard, which reads this same pbpaste output). The
	// only cost is transiently buffering an unusually large text clipboard before
	// discarding it; the correct outcome (no undo offered) is unaffected. A
	// streaming capped-exec helper would be a reasonable cross-cutting change, but
	// single-casing it here would diverge from that shared path for a marginal,
	// pathological case.
	pbpaste, err := policy.ResolveBinary("pbpaste")
	if err != nil {
		return nil, err
	}
	priorRes, err := runCommand(ctx, pbpaste, "-Prefer", "txt")
	if err != nil {
		return nil, fmt.Errorf("write_clipboard: reading current clipboard for undo: %w", err)
	}
	if err := commandFailed("pbpaste", priorRes); err != nil {
		return nil, fmt.Errorf("write_clipboard: %w", err)
	}
	return planWriteClipboard(text, []byte(priorRes.Stdout))
}

// planWriteClipboard is the pure staging half of stageWriteClipboard: given the
// new text and the clipboard's already-probed prior contents, it assembles the
// forward/inverse commands and the preview. Splitting the decision out from the
// pbpaste probe makes every branch — oversized-text rejection, and the two cases
// that yield no undo — unit-testable without touching the live clipboard.
func planWriteClipboard(text string, prior []byte) (*StagedPlan, error) {
	if len(text) > maxClipboardBytes {
		return nil, fmt.Errorf("write_clipboard: text is %d bytes, exceeding the %d-byte limit", len(text), maxClipboardBytes)
	}

	forward := Command{Binary: "pbcopy", Args: nil, Stdin: []byte(text), DiscardStdout: true}
	preview := fmt.Sprintf("Replace the clipboard with %s.", previewSnippet([]byte(text)))

	// Decide whether a faithful undo is possible. An empty pbpaste result means
	// the prior clipboard was empty or non-text (image/file) — either way there is
	// no text to restore to, and clearing the clipboard would not bring a
	// non-text item back. An oversized prior payload is declined as an undo too.
	switch {
	case len(prior) == 0:
		return &StagedPlan{
			Preview: preview + " Undo is not available: the clipboard was empty or held non-text content (e.g. an image), so there is nothing to restore.",
			Forward: forward,
			Inverse: nil,
		}, nil
	case len(prior) > maxClipboardBytes:
		return &StagedPlan{
			Preview: fmt.Sprintf("%s Undo is not available: the prior clipboard held %d bytes, more than the %d-byte limit kept for undo.",
				preview, len(prior), maxClipboardBytes),
			Forward: forward,
			Inverse: nil,
		}, nil
	default:
		return &StagedPlan{
			Preview: fmt.Sprintf("%s Undo will restore the previous clipboard text (%d bytes).", preview, len(prior)),
			Forward: forward,
			// The inverse carries the prior clipboard text as stdin; DiscardStdout
			// keeps undo from echoing that (possibly sensitive) prior value back —
			// pbcopy emits nothing anyway, but this makes the intent explicit.
			Inverse: &Command{Binary: "pbcopy", Args: nil, Stdin: prior, DiscardStdout: true},
		}, nil
	}
}
