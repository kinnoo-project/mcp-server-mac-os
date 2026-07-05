// builtins_security.go implements the read-only "app trust & integrity" builtins
// of the security domain — the operations that answer "can I trust this app, and
// is my Mac's own protection turned on?":
//
//   - verify_signature — is this app/binary code-signed, and by whom? (codesign)
//   - gatekeeper_check  — would Gatekeeper let this app run? (spctl --assess)
//   - sip_status        — is System Integrity Protection enabled? (csrutil status)
//   - quarantine_info   — was this file downloaded from the internet? (xattr)
//
// # Why these binaries are usable despite their power
//
// csrutil and spctl are removed from the registry deny list to reach these
// operations (see internal/registry/security_invariants_test.go). That removal is
// only safe because every capability here pins the binary to a single, read-only
// verb and can never reach its state-changing sub-commands: codesign runs only in
// verify/display mode (never sign), spctl only with --assess (never --add /
// --enable / --master-disable), csrutil only with "status" (never disable/enable,
// which in any case require Recovery mode), and xattr only with -p to PRINT the
// attribute (never -w to write or -d to delete). Those pins are the argv-builder
// functions below (codesignVerifyArgs, spctlAssessArgs, csrutilStatusArgs,
// xattrQuarantineArgs), and they are asserted verbatim by the verb-pinning
// invariant TestSecurity_ConstrainedBinaryVerbs — the reusable frame the storage
// domain (V7) extends. Keeping argv assembly in these tiny pure functions is what
// lets the test prove the pins without launching the tools.
//
// # Injection posture
//
// The three path-taking operations feed a model-controlled path to a subprocess.
// codesign and spctl have NO usable "--" end-of-options terminator, so the sole
// defense there is validateExistingOperand, which rejects a dash-leading value
// and resolves the path to ABSOLUTE form (it then starts with "/") before it can
// reach argv — so it can never be read as a flag. xattr DOES honour "--", so its
// path additionally rides after a "--" terminator as defense in depth (and is
// validated the same way). sip_status takes no input at all. See
// builtins_security_test.go for the per-operation hostile-path regressions.
package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// codesignVerifyArgs is the pinned argument vector for verify_signature. codesign
// is asked to BOTH verify the signature is intact (--verify --deep --strict) and
// display the signing details (-dvvv, which prints the authority chain and Team
// ID to stderr). It is never asked to sign: no "-s"/"--sign" verb can appear here.
// The path is placed last; codesign has no "--" terminator, so the caller MUST
// have run validateExistingOperand first (which is what keeps a dash-leading value
// from being read as a flag). Split out as a pure function so the verb-pinning
// invariant can assert the exact argv shape without invoking codesign.
func codesignVerifyArgs(path string) []string {
	return []string{"--verify", "--deep", "--strict", "-dvvv", path}
}

// spctlAssessArgs is the pinned argument vector for gatekeeper_check. spctl is
// invoked ONLY with --assess (the read-only "would Gatekeeper allow this?" query);
// it can never reach the rule-changing verbs (--add, --enable, --disable,
// --master-disable). --type exec assesses it as an executable and -vv makes spctl
// print the matched source (Notarized Developer ID, App Store, …). Path last;
// spctl has no "--" terminator, so validateExistingOperand is the guard.
func spctlAssessArgs(path string) []string {
	return []string{"--assess", "--type", "exec", "-vv", path}
}

// csrutilStatusArgs is the pinned argument vector for sip_status: csrutil is only
// ever invoked with the read-only "status" verb, never "enable"/"disable"/"clear"
// (which require Recovery mode anyway). It takes no model input.
func csrutilStatusArgs() []string {
	return []string{"status"}
}

// xattrQuarantineArgs is the pinned argument vector for quarantine_info: xattr is
// invoked with -p (PRINT the named attribute) only — never -w (write) or -d
// (delete) or -c (clear). The attribute name is a fixed constant; the path rides
// after a "--" terminator (xattr honours it) and is also validated up front.
func xattrQuarantineArgs(path string) []string {
	return []string{"-p", "com.apple.quarantine", "--", path}
}

// runVerifySignature reports whether an app or binary is code-signed and, if so,
// its signing authority chain and Team ID. codesign writes its display output to
// stderr and exits non-zero when the item is unsigned or its signature is broken,
// so the exit code drives the verdict and stderr carries the detail — both are
// surfaced as data (an unsigned app is a legitimate answer, not a transport error).
func runVerifySignature(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	raw, _ := getString(in, "path")
	path, err := validateExistingOperand("verify_signature", "path", raw)
	if err != nil {
		return "", err
	}
	bin, err := policy.ResolveBinary("codesign")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, codesignVerifyArgs(path)...)
	if err != nil {
		return "", err
	}
	// codesign prints the signing details (Authority=, TeamIdentifier=, …) to
	// stderr in both the success and failure cases; stdout is normally empty.
	detail := strings.TrimSpace(res.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(res.Stdout)
	}
	var b strings.Builder
	if res.ExitCode == 0 {
		fmt.Fprintf(&b, "Signature for %s: VALID (the code is signed and has not been tampered with).\n\n", path)
	} else {
		fmt.Fprintf(&b, "Signature for %s: NOT valid — the item is either unsigned or its signature is broken/altered.\n\n", path)
	}
	if detail == "" {
		b.WriteString("(codesign reported no further detail.)")
	} else {
		b.WriteString(compactOutput(detail))
	}
	return b.String(), nil
}

// runGatekeeperCheck reports whether macOS Gatekeeper would allow an app to run,
// via `spctl --assess`. spctl exits 0 when the item is accepted and non-zero when
// rejected, printing the verdict and matched source to stderr; both are surfaced
// as data so "rejected" reads as the answer, not a failure.
func runGatekeeperCheck(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	raw, _ := getString(in, "path")
	path, err := validateExistingOperand("gatekeeper_check", "path", raw)
	if err != nil {
		return "", err
	}
	bin, err := policy.ResolveBinary("spctl")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, spctlAssessArgs(path)...)
	if err != nil {
		return "", err
	}
	detail := strings.TrimSpace(res.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(res.Stdout)
	}
	var b strings.Builder
	if res.ExitCode == 0 {
		fmt.Fprintf(&b, "Gatekeeper would ALLOW %s to run (accepted).\n\n", path)
	} else {
		fmt.Fprintf(&b, "Gatekeeper would BLOCK %s (rejected) — opening it would require an explicit override.\n\n", path)
	}
	if detail == "" {
		b.WriteString("(spctl reported no further detail.)")
	} else {
		b.WriteString(compactOutput(detail))
	}
	return b.String(), nil
}

// runSipStatus reports whether System Integrity Protection is enabled, from
// `csrutil status`. Fixed argv, no model input, no injection surface.
func runSipStatus(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("csrutil")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, csrutilStatusArgs()...)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		out = strings.TrimSpace(res.Stderr)
	}
	if out == "" {
		return "csrutil reported no System Integrity Protection status.", nil
	}
	return "System Integrity Protection:\n" + out, nil
}

// runQuarantineInfo reports whether a file carries the macOS quarantine flag. When
// the attribute is present, xattr prints the raw quarantine string, which
// renderQuarantine decodes into who downloaded the file and when. When it is
// absent, xattr exits non-zero with a "No such xattr" message — that is the
// common, benign "not quarantined" case and is reported as such, not as an error.
func runQuarantineInfo(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	raw, _ := getString(in, "path")
	path, err := validateExistingOperand("quarantine_info", "path", raw)
	if err != nil {
		return "", err
	}
	bin, err := policy.ResolveBinary("xattr")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, xattrQuarantineArgs(path)...)
	if err != nil {
		return "", err
	}
	return interpretQuarantineResult(path, res)
}

// interpretQuarantineResult renders the outcome of `xattr -p com.apple.quarantine`
// into a report. It is the security-relevant seam of quarantine_info, so it is a
// pure function with its own accept/reject tests. It separates THREE cases that a
// naive "non-zero exit ⇒ not quarantined" check would conflate:
//
//   - the attribute is PRESENT (exit 0 with a value): decode it;
//   - the attribute is simply ABSENT: xattr exits non-zero and prints
//     "No such xattr", the common, benign not-quarantined case;
//   - ANY OTHER failure — permission denied, an I/O error, a file that vanished
//     after validation — which must surface as an ERROR, never be reported as
//     "not quarantined". Silently calling a failed read "not quarantined" could
//     hide a real download flag and mislead a trust decision (Copilot review, PR #62).
func interpretQuarantineResult(path string, res *runResult) (string, error) {
	value := strings.TrimSpace(res.Stdout)
	if res.ExitCode == 0 && value != "" {
		return renderQuarantine(path, value), nil
	}
	stderr := strings.TrimSpace(res.Stderr)
	// "No such xattr" is xattr's message for an unset attribute; a clean exit-0
	// with empty output is treated the same way (an odd but harmless empty value).
	if strings.Contains(stderr, "No such xattr") || (res.ExitCode == 0 && value == "") {
		return fmt.Sprintf("%s is NOT quarantined — macOS has no download-origin flag on it, so it will open without a Gatekeeper prompt.", path), nil
	}
	detail := stderr
	if detail == "" {
		detail = fmt.Sprintf("xattr exited with status %d and produced no detail", res.ExitCode)
	}
	return "", fmt.Errorf("quarantine_info: could not read the quarantine attribute of %q: %s", path, detail)
}

// renderQuarantine decodes a raw com.apple.quarantine attribute value into a
// human-readable report. The value is a semicolon-separated record macOS writes
// when it quarantines a download: "<flags>;<hex-unix-time>;<agent>;<event-uuid>".
// The agent (the app that downloaded the file) and the timestamp are the useful
// fields; the raw value is included for completeness. It is a pure function so it
// can be unit-tested against canned attribute strings.
func renderQuarantine(path, value string) string {
	parts := strings.Split(value, ";")
	var b strings.Builder
	fmt.Fprintf(&b, "%s IS quarantined — macOS flagged it as downloaded from the internet, so opening it triggers a Gatekeeper confirmation.\n", path)
	if len(parts) >= 3 {
		if agent := strings.TrimSpace(parts[2]); agent != "" {
			fmt.Fprintf(&b, "Downloaded by: %s\n", agent)
		}
	}
	if len(parts) >= 2 {
		if ts := strings.TrimSpace(parts[1]); ts != "" {
			if when, ok := decodeQuarantineTime(ts); ok {
				fmt.Fprintf(&b, "Downloaded at: %s\n", when)
			}
		}
	}
	fmt.Fprintf(&b, "Raw quarantine attribute: %s", value)
	return b.String()
}

// decodeQuarantineTime interprets the quarantine record's timestamp field, which
// macOS stores as hexadecimal seconds since the Unix epoch, and renders it in the
// local timezone. ok is false when the field is not parseable hex.
func decodeQuarantineTime(hexTime string) (string, bool) {
	secs, err := strconv.ParseInt(strings.TrimSpace(hexTime), 16, 64)
	if err != nil {
		return "", false
	}
	return time.Unix(secs, 0).Format("2006-01-02 15:04:05 MST"), true
}
