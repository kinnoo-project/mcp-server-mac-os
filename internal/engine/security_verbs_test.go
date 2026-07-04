// security_verbs_test.go is a production security gate: it pins the exact verb
// each constrained system binary may be invoked with.
//
// Four binaries the registry deny list would otherwise block — codesign, spctl,
// csrutil, xattr — are reachable through the security domain (V5). That is only
// safe because each is used in a single, read-only mode and can never reach its
// state-changing sub-commands (spctl --add, csrutil disable, xattr -w, a codesign
// signing verb). This file makes that promise an ENFORCED invariant rather than a
// code-review nicety: it asserts, against the actual argv-builder functions in
// builtins_security.go, that every constrained binary's command starts with an
// allowed verb and contains none of the forbidden ones.
//
// It is the reusable frame the storage domain (V7) extends when it un-denies
// tmutil, diskutil, and hdiutil: add a row to constrainedBinaryVerbs and a probe
// to the argv table below.
package engine

import (
	"strings"
	"testing"
)

// constrainedBinaryVerbs records, for each binary that was removed from (or would
// belong on) the registry deny list, the closed set of first-argument "verbs" any
// capability may use it with, plus the flags/verbs that must NEVER appear anywhere
// in its argv because they would change system state. The security domain confines
// each binary to exactly one read-only mode.
var constrainedBinaryVerbs = map[string]struct {
	allowedFirstArgs []string // the argv[0] verb must be one of these
	forbiddenTokens  []string // none of these may appear anywhere in argv
}{
	// codesign: verify/display only. A signing invocation would carry -s/--sign;
	// --remove-signature strips one. Neither may ever appear.
	"codesign": {
		allowedFirstArgs: []string{"--verify"},
		forbiddenTokens:  []string{"-s", "--sign", "--remove-signature"},
	},
	// spctl: assessment only. The rule/state-changing verbs are --add, --remove,
	// --enable, --disable and the global kill-switch --master-disable.
	"spctl": {
		allowedFirstArgs: []string{"--assess"},
		forbiddenTokens:  []string{"--add", "--remove", "--enable", "--disable", "--master-disable", "--master-enable"},
	},
	// csrutil: status only. Everything that mutates SIP — enable/disable/clear —
	// is forbidden (and would require Recovery mode regardless).
	"csrutil": {
		allowedFirstArgs: []string{"status"},
		forbiddenTokens:  []string{"disable", "enable", "clear", "netboot"},
	},
	// xattr: print only. Writing (-w), deleting (-d) and clearing (-c) an
	// attribute all change the file and must never appear.
	"xattr": {
		allowedFirstArgs: []string{"-p"},
		forbiddenTokens:  []string{"-w", "-d", "-c"},
	},
}

// TestSecurity_ConstrainedBinaryVerbs asserts every constrained binary's real
// argv (as produced by the builtins' argv-builder functions) starts with an
// allowed verb and never contains a forbidden, state-changing token. A benign
// absolute path is passed where the operation takes one; because the guards are
// about the verb and flags, the path value is irrelevant to what this proves.
func TestSecurity_ConstrainedBinaryVerbs(t *testing.T) {
	const samplePath = "/Applications/Sample.app"

	// Each row is one capability's actual argv for a constrained binary. Adding a
	// capability over a constrained binary means adding a row here (the coverage
	// check below fails if a constrained binary has no row at all).
	cases := []struct {
		capability string
		binary     string
		argv       []string
	}{
		{"verify_signature", "codesign", codesignVerifyArgs(samplePath)},
		{"gatekeeper_check", "spctl", spctlAssessArgs(samplePath)},
		{"sip_status", "csrutil", csrutilStatusArgs()},
		{"quarantine_info", "xattr", xattrQuarantineArgs(samplePath)},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		rule, ok := constrainedBinaryVerbs[tc.binary]
		if !ok {
			t.Errorf("%s: binary %q has no verb-pinning rule; add one to constrainedBinaryVerbs", tc.capability, tc.binary)
			continue
		}
		covered[tc.binary] = true

		if len(tc.argv) == 0 {
			t.Errorf("%s: empty argv for %q", tc.capability, tc.binary)
			continue
		}
		if !contains(rule.allowedFirstArgs, tc.argv[0]) {
			t.Errorf("%s: %q invoked with first-arg %q, want one of %v (the read-only verb pin)",
				tc.capability, tc.binary, tc.argv[0], rule.allowedFirstArgs)
		}
		for _, tok := range rule.forbiddenTokens {
			for _, a := range tc.argv {
				if a == tok {
					t.Errorf("%s: %q argv contains forbidden state-changing token %q (argv: %v)",
						tc.capability, tc.binary, tok, tc.argv)
				}
			}
		}
	}

	// Every binary we bothered to constrain must have at least one probing row,
	// so the pin cannot silently stop being exercised.
	for bin := range constrainedBinaryVerbs {
		if !covered[bin] {
			t.Errorf("constrainedBinaryVerbs pins %q but no case exercises its argv — add a probe row", bin)
		}
	}
}

// contains reports whether v is in s.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestSecurity_QuarantineArgsHaveTerminator is an extra belt-and-braces check on
// the one constrained binary that DOES honour "--": xattr's path must ride after
// the terminator so even a (already-rejected) dash-leading value could not be read
// as a flag. codesign and spctl lack "--" and rely on absolute-path resolution
// instead, so they are intentionally not asserted here.
func TestSecurity_QuarantineArgsHaveTerminator(t *testing.T) {
	argv := xattrQuarantineArgs("/tmp/x")
	term := indexOf(argv, "--")
	if term < 0 {
		t.Fatalf("xattr argv has no '--' terminator: %v", argv)
	}
	if !appearsAfter(argv, "/tmp/x", term) {
		t.Errorf("xattr path does not appear after the '--' terminator: %v", argv)
	}
	// The attribute name must be the constant we pin — never model-controlled.
	if !strings.Contains(strings.Join(argv, " "), "com.apple.quarantine") {
		t.Errorf("xattr argv does not query the fixed com.apple.quarantine attribute: %v", argv)
	}
}
