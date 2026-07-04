// builtins_security_test.go covers the security domain's app-trust builtins:
// the argv-verb pins (also exercised by security_verbs_test.go), the per-operation
// hostile-path regressions required by CLAUDE.md §4, and the pure quarantine
// decoder. A live path that shells out to the real codesign/spctl/csrutil/xattr is
// gated behind MCP_SECURITY_LIVE=1 because its output depends on host state.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mcp-server-mac-os/internal/registry"
)

// TestSecurityArgvBuilders pins the exact argument vectors so a future edit that
// slipped in a state-changing verb (or dropped the read-only pin) fails here in
// addition to TestSecurity_ConstrainedBinaryVerbs.
func TestSecurityArgvBuilders(t *testing.T) {
	if got, want := strings.Join(codesignVerifyArgs("/p"), " "), "--verify --deep --strict -dvvv /p"; got != want {
		t.Errorf("codesignVerifyArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(spctlAssessArgs("/p"), " "), "--assess --type exec -vv /p"; got != want {
		t.Errorf("spctlAssessArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(csrutilStatusArgs(), " "), "status"; got != want {
		t.Errorf("csrutilStatusArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(xattrQuarantineArgs("/p"), " "), "-p com.apple.quarantine -- /p"; got != want {
		t.Errorf("xattrQuarantineArgs = %q, want %q", got, want)
	}
}

// TestSecurity_HostilePathRejected is the per-operation injection regression: a
// dash-leading path (the classic "-e"-as-a-flag payload) must be rejected up front
// by validateExistingOperand for every path-taking security op, never assembled
// into argv where codesign/spctl could read it as one of their own flags.
func TestSecurity_HostilePathRejected(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	ops := map[string]BuiltinFunc{
		"verify_signature": runVerifySignature,
		"gatekeeper_check": runGatekeeperCheck,
		"quarantine_info":  runQuarantineInfo,
	}
	// A leading-dash value and a bare dash are the flag-injection shapes; both
	// must be refused before any subprocess runs.
	for _, hostile := range []string{"-e", "-rf", "--flood", "-"} {
		for name, fn := range ops {
			_, err := fn(ctx, cap, map[string]any{"path": hostile})
			if err == nil {
				t.Errorf("%s(%q): expected rejection of a dash-leading path, got nil error", name, hostile)
				continue
			}
			if !strings.Contains(err.Error(), "'-'") {
				t.Errorf("%s(%q): expected a dash-guard error mentioning '-', got %v", name, hostile, err)
			}
		}
	}
}

// TestSecurity_NonexistentPathRejected confirms the path-taking ops fail cleanly
// (with a "does not exist" message) rather than shelling out with a path that
// isn't there, so the model gets an actionable error instead of an opaque tool
// failure.
func TestSecurity_NonexistentPathRejected(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	missing := filepath.Join(t.TempDir(), "no-such-file")
	for name, fn := range map[string]BuiltinFunc{
		"verify_signature": runVerifySignature,
		"gatekeeper_check": runGatekeeperCheck,
		"quarantine_info":  runQuarantineInfo,
	} {
		if _, err := fn(ctx, cap, map[string]any{"path": missing}); err == nil ||
			!strings.Contains(err.Error(), "does not exist") {
			t.Errorf("%s: expected a 'does not exist' error for a missing path, got %v", name, err)
		}
	}
}

// TestDecodeQuarantineTime checks the hex-seconds decode used for the "downloaded
// at" line: a valid hex timestamp round-trips to the same instant time.Unix would
// render, and a non-hex value is refused (ok=false) so it is simply omitted.
func TestDecodeQuarantineTime(t *testing.T) {
	const unix int64 = 1700000000
	hexTime := strconv.FormatInt(unix, 16)
	got, ok := decodeQuarantineTime(hexTime)
	if !ok {
		t.Fatalf("decodeQuarantineTime(%q) ok=false, want true", hexTime)
	}
	if want := time.Unix(unix, 0).Format("2006-01-02 15:04:05 MST"); got != want {
		t.Errorf("decodeQuarantineTime(%q) = %q, want %q", hexTime, got, want)
	}
	if _, ok := decodeQuarantineTime("not-hex-zz"); ok {
		t.Errorf("decodeQuarantineTime on a non-hex value: ok=true, want false")
	}
}

// TestRenderQuarantine checks the human-readable rendering of a raw quarantine
// attribute: the "IS quarantined" verdict, the downloading agent, and the raw
// value are all surfaced. A short/degenerate value must still render the verdict
// without panicking on the missing fields.
func TestRenderQuarantine(t *testing.T) {
	// Format: flags;hex-unix-time;agent;event-uuid. UUID is a placeholder, not PII.
	full := "0083;6553f100;Safari;1F2E3D4C-0000-0000-0000-000000000000"
	out := renderQuarantine("/tmp/installer.dmg", full)
	for _, want := range []string{"IS quarantined", "Downloaded by: Safari", "Raw quarantine attribute:", "Downloaded at:"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderQuarantine full value missing %q; got:\n%s", want, out)
		}
	}

	// A degenerate one-field value must not panic and must still state the verdict.
	short := renderQuarantine("/tmp/x", "0001")
	if !strings.Contains(short, "IS quarantined") {
		t.Errorf("renderQuarantine short value missing verdict; got:\n%s", short)
	}
	if strings.Contains(short, "Downloaded by:") {
		t.Errorf("renderQuarantine short value should not fabricate an agent; got:\n%s", short)
	}
}

// TestInterpretQuarantineResult pins the three-way branching that a security
// trust decision depends on: a present attribute is decoded, a genuinely absent
// one ("No such xattr") reads as not-quarantined, but ANY other failure (e.g. a
// permission error) surfaces as an error rather than being misreported as
// "not quarantined" (Copilot review, PR #62).
func TestInterpretQuarantineResult(t *testing.T) {
	const path = "/tmp/x"

	// Present: exit 0 with a value → decoded quarantine report.
	if out, err := interpretQuarantineResult(path, &runResult{Stdout: "0083;6553f100;Safari;UUID", ExitCode: 0}); err != nil {
		t.Errorf("present attribute: unexpected error %v", err)
	} else if !strings.Contains(out, "IS quarantined") {
		t.Errorf("present attribute: want quarantined verdict, got %q", out)
	}

	// Absent: xattr's "No such xattr" on a non-zero exit → not quarantined.
	if out, err := interpretQuarantineResult(path, &runResult{
		Stderr:   "xattr: /tmp/x: No such xattr: com.apple.quarantine",
		ExitCode: 1,
	}); err != nil {
		t.Errorf("absent attribute: unexpected error %v", err)
	} else if !strings.Contains(out, "NOT quarantined") {
		t.Errorf("absent attribute: want not-quarantined verdict, got %q", out)
	}

	// Other failure: a permission error must NOT be reported as not-quarantined.
	out, err := interpretQuarantineResult(path, &runResult{
		Stderr:   "xattr: [Errno 13] Permission denied: '/tmp/x'",
		ExitCode: 1,
	})
	if err == nil {
		t.Errorf("permission error: expected an error, got output %q", out)
	} else if strings.Contains(err.Error(), "NOT quarantined") {
		t.Errorf("permission error: must not claim not-quarantined, got %v", err)
	}
}

// TestSecurityBuiltins_Live exercises the real binaries end-to-end. Skipped unless
// MCP_SECURITY_LIVE=1 because it shells out and its output depends on host state.
// /bin/ls is a stable, always-present, Apple-signed target for the signing/assess
// probes; sip_status and a not-quarantined file round out the read paths.
func TestSecurityBuiltins_Live(t *testing.T) {
	if os.Getenv("MCP_SECURITY_LIVE") != "1" {
		t.Skip("set MCP_SECURITY_LIVE=1 to run the live security builtins")
	}
	ctx := context.Background()
	cap := registry.Capability{}

	if out, err := runVerifySignature(ctx, cap, map[string]any{"path": "/bin/ls"}); err != nil {
		t.Errorf("runVerifySignature: %v", err)
	} else if !strings.Contains(out, "Signature for") {
		t.Errorf("verify_signature missing header, got: %s", out)
	}
	if out, err := runGatekeeperCheck(ctx, cap, map[string]any{"path": "/bin/ls"}); err != nil {
		t.Errorf("runGatekeeperCheck: %v", err)
	} else if !strings.Contains(out, "Gatekeeper") {
		t.Errorf("gatekeeper_check missing header, got: %s", out)
	}
	if out, err := runSipStatus(ctx, cap, nil); err != nil {
		t.Errorf("runSipStatus: %v", err)
	} else if !strings.Contains(out, "System Integrity Protection") {
		t.Errorf("sip_status missing header, got: %s", out)
	}
	// /bin/ls is a system binary, never downloaded, so it is not quarantined.
	if out, err := runQuarantineInfo(ctx, cap, map[string]any{"path": "/bin/ls"}); err != nil {
		t.Errorf("runQuarantineInfo: %v", err)
	} else if !strings.Contains(out, "NOT quarantined") {
		t.Errorf("quarantine_info on /bin/ls expected NOT quarantined, got: %s", out)
	}
}
