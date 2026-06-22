// messages_sandbox.go stages outgoing iMessage attachments into a location that
// Messages.app is actually able to read.
//
// # Why this exists — the sandbox-read constraint
//
// Messages.app runs inside the macOS App Sandbox. When send_message hands it a
// file path via AppleScript, it is Messages — not this server — that must open
// and upload the file. A path OUTSIDE Messages' sandbox (for example /tmp or
// ~/Downloads) is unreadable to it: the attachment is accepted into the
// conversation and then silently fails to transmit. In the Messages database
// this shows up as the attachment's transfer_state = 6 (failed) and the
// message's error = 3, while plain text to the same recipient sends fine.
//
// This was reproduced and diagnosed empirically on macOS 15.6: files sent from
// ~/Downloads failed every time, while a copy of the SAME file placed inside
// Messages' sandbox uploaded cleanly (transfer_state = 5, is_sent = 1). The full
// investigation is recorded in docs/issues/note-imessage-attachments-design.md
// and docs/issues/bug-imessage-attachment-send-fails.md.
//
// The fix is therefore not an AppleScript change but a file-location change: copy
// each attachment into a directory inside Messages' OWN sandbox container before
// sending, so the app is guaranteed read access. The copy is scratch — it is
// created at stage time (nothing is sent until execute) and reclaimed by a sweep
// on a later send — so the only externally visible effect remains the message
// the user explicitly confirms.
//
// # Load-bearing assumption: the Messages sandbox container id
//
// We hardcode Messages.app's bundle identifier, "com.apple.MobileSMS", to locate
// its sandbox container at
//
//	~/Library/Containers/com.apple.MobileSMS/Data/tmp/
//
// This identifier has been stable across many macOS releases (it predates the
// "Messages" rename and is still the iMessage process's bundle id today). It is
// the single OS-version assumption in the attachment path, called out here and
// in docs/issues so the trade-off is discoverable. If Apple ever changes it, the
// container path will simply be absent and stageAttachmentsIntoSandbox returns a
// clear error so the send is refused up front — rather than silently failing to
// transmit the way the original external-path bug did. Text-only sends, which
// need no copy, are unaffected either way.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// messagesSandboxBundleID is Messages.app's bundle identifier, used to locate its
// App Sandbox container. See the file header for why this is hardcoded and what
// happens if it ever changes.
const messagesSandboxBundleID = "com.apple.MobileSMS"

// messagesStagingDirName is the subdirectory, inside Messages' sandbox tmp, that
// holds this server's per-send attachment copies. Namespacing under a single
// directory keeps the sweep simple and scoped to files we created.
const messagesStagingDirName = "mcp-send"

// messagesStagingMaxAge bounds how long a staged copy lives before a later send
// sweeps it. It must comfortably exceed the time Messages needs to finish
// uploading an attachment after the send fires (seconds), while still keeping
// disk use bounded; one hour is far more than enough for the former.
const messagesStagingMaxAge = time.Hour

// messagesSandboxTmpDir overrides the location attachment copies are staged into.
// It is a package var, not a constant, so tests can redirect staging to a
// t.TempDir() instead of writing into the real Messages container. Empty (the
// production value) means "derive from $HOME at call time".
var messagesSandboxTmpDir = ""

// resolveMessagesSandboxTmpDir returns the directory inside Messages' sandbox
// container where attachment copies are staged, honoring the test override when
// set.
func resolveMessagesSandboxTmpDir() (string, error) {
	if messagesSandboxTmpDir != "" {
		return messagesSandboxTmpDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Containers", messagesSandboxBundleID, "Data", "tmp"), nil
}

// stageAttachmentsIntoSandbox copies each attachment into a fresh, uniquely named
// subdirectory of Messages' sandbox tmp and returns the copied paths in the same
// order, for the AppleScript send to reference instead of the originals. Each
// file keeps its original basename (so the recipient sees a sensible filename)
// and is isolated in its own index subdirectory, so two attachments sharing a
// basename never collide.
//
// Callers must have already validated that every path is an existing regular
// file. Before copying, the function sweeps stale staging directories from
// earlier sends, bounding disk use without ever deleting a copy a still-in-flight
// upload might need.
func stageAttachmentsIntoSandbox(attachments []string) ([]string, error) {
	base, err := resolveMessagesSandboxTmpDir()
	if err != nil {
		return nil, err
	}
	// The sandbox container must already exist — Messages cannot be sending
	// iMessages without it. Failing clearly here (rather than copying into a path
	// Messages still can't read) is the documented behavior if the hardcoded
	// bundle id ever stops matching reality.
	if _, err := os.Stat(base); err != nil {
		return nil, fmt.Errorf("Messages sandbox container not found at %s; cannot stage attachments: %w", base, err)
	}

	stagingRoot := filepath.Join(base, messagesStagingDirName)
	sweepStaleStagingDirs(stagingRoot, messagesStagingMaxAge)

	id, err := randomHex()
	if err != nil {
		return nil, err
	}
	sendDir := filepath.Join(stagingRoot, id)

	staged := make([]string, 0, len(attachments))
	for i, src := range attachments {
		// Own subdirectory per attachment so identical basenames can't overwrite
		// one another within a single send.
		dstDir := filepath.Join(sendDir, strconv.Itoa(i))
		if err := os.MkdirAll(dstDir, 0o700); err != nil {
			return nil, fmt.Errorf("creating attachment staging dir: %w", err)
		}
		dst := filepath.Join(dstDir, filepath.Base(src))
		if err := copyRegularFile(src, dst); err != nil {
			return nil, fmt.Errorf("staging attachment %q: %w", src, err)
		}
		staged = append(staged, dst)
	}
	return staged, nil
}

// sweepStaleStagingDirs removes staging subdirectories older than maxAge, judged
// by each directory's modification time (set when it was created for a prior
// send). It is best-effort: any error — including the root not existing yet — is
// ignored, because a failed sweep must never block a send. This is the
// "staging directory" reclamation discipline from
// .claude/rules/transactional-state.md applied to Messages' scratch copies.
func sweepStaleStagingDirs(root string, maxAge time.Duration) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// copyRegularFile copies src to dst with owner-only permissions. The caller has
// already verified src is an existing regular file; dst's parent already exists.
func copyRegularFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	// On the copy-failure path the close error is not discarded: closing a
	// writable file can itself report a deferred write/flush failure, so if both
	// fail we surface both rather than hiding the close error behind the copy
	// error. On success the close error is the only signal that the bytes were
	// durably written, so it is returned directly.
	if _, copyErr := io.Copy(out, in); copyErr != nil {
		if closeErr := out.Close(); closeErr != nil {
			return fmt.Errorf("%w (and closing destination failed: %v)", copyErr, closeErr)
		}
		return copyErr
	}
	return out.Close()
}

// randomHex returns a 128-bit random value as hex, giving each send's staging
// directory a unique, unpredictable name.
func randomHex() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating staging dir id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
