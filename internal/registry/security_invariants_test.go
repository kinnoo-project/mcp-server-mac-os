// security_invariants_test.go is part of the production security gate (see
// docs/TESTS.md). Where registry_test.go checks that the shipped manifests are
// structurally well-formed, this file checks that they are SAFE: that the set of
// operations the model can reach cannot silently grow to include a
// lockout/destroy/DoS-class tool, and that every irrecoverable operation stays
// behind the human-approval gate.
//
// These are deliberately asserted against the real, embedded manifests
// (registry.Load) rather than hand-built fixtures: the whole point is to fail
// the build the moment a future manifest edit crosses a safety boundary, even if
// that edit is otherwise structurally valid.
package registry

import (
	"testing"

	"mcp-server-mac-os/internal/policy"
)

// deniedBinaries are high-blast-radius system tools that NO capability may
// invoke. None is used by any current capability; listing them here turns "the
// attack surface grew to include a way to wipe a disk / lock the user out /
// disable a security control" into a visible test failure rather than something
// a reviewer must catch by eye.
//
// Note the asymmetry with binaries that ARE used: pmset, launchctl,
// networksetup, defaults, and kill are all individually capable of dangerous
// changes, but each is reached only through a narrowly-constrained capability
// (read-only status probes, the curated write_setting allowlist, the
// SIGTERM-only terminate_process) whose own guards are tested elsewhere. The
// deny list therefore names tools that have no such constrained use today;
// adding one back is a conscious, reviewable edit to this list.
//
// csrutil and spctl were on this list until the security domain (V5) added
// read-only app-trust probes over them (sip_status, gatekeeper_check); tmutil,
// diskutil, and hdiutil came off it when the storage domain (V7) added Time
// Machine / volume / disk-image operations. None of the five is re-added: their
// safety now rests on verb pinning instead — the engine invokes each ONLY with a
// closed set of read-only or benign sub-commands (csrutil "status", spctl
// "--assess", tmutil status/list*, diskutil list/info/mount, hdiutil
// attach/detach) and can never reach a state-changing verb (diskutil erase,
// tmutil delete, hdiutil create, …). That pin is asserted by
// engine.TestSecurity_ConstrainedBinaryVerbs, the moved-forward equivalent of a
// deny-list entry for a binary that now has one constrained use.
var deniedBinaries = map[string]bool{
	"rm": true, "rmdir": true, "dd": true, "mkfs": true, "newfs": true,
	"asr": true, "fsck": true,
	"nvram": true, "fdesetup": true,
	"dscl": true, "killall": true, "shutdown": true, "reboot": true,
	"halt": true, "chflags": true, "ditto": true,
}

// dangerousOps are the irreversible, high-blast-radius operations whose entire
// safety story rests on always being staged for explicit human confirmation
// (they send external communications, spend physical resources, or terminate
// processes). This test pins their risk/reversibility/gating metadata so a
// future edit cannot quietly downgrade one into an auto-firing operation.
var dangerousOps = []string{
	"send_mail", "send_message", "call",
	"print_file", "print_test_page",
	"quit_process", "terminate_process",
	// run_shortcut (V8) runs arbitrary user-authored automation: unbounded blast
	// radius, no meaningful inverse. Its high/irreversible/staged classification
	// must never be softened, so it is pinned here alongside the other
	// irrecoverable operations.
	"run_shortcut",
}

func loadRegistry(t *testing.T) *Registry {
	t.Helper()
	reg, err := Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	return reg
}

// TestSecurity_NoDeniedBinaries fails if any capability references a binary on
// the deny list — the core "the surface cannot grow to include a destructive
// tool" guard.
func TestSecurity_NoDeniedBinaries(t *testing.T) {
	for _, c := range loadRegistry(t).All() {
		if c.Binary != "" && deniedBinaries[c.Binary] {
			t.Errorf("capability %q references denied binary %q: high-blast-radius tools must never be reachable", c.Name, c.Binary)
		}
	}
}

// TestSecurity_AllBinariesResolveToTrustedDirs confirms every binary a manifest
// names actually resolves through the policy trust boundary to one of the
// trusted Darwin system directories. This is enforced per-call at runtime; doing
// it here as well means a manifest that names a typo'd or non-system binary
// fails the build instead of only failing the first time a user invokes it.
func TestSecurity_AllBinariesResolveToTrustedDirs(t *testing.T) {
	for _, c := range loadRegistry(t).All() {
		if c.Binary == "" {
			continue // builtin: answered in-process, no subprocess
		}
		if _, err := policy.ResolveBinary(c.Binary); err != nil {
			t.Errorf("capability %q binary %q does not resolve to a trusted system directory: %v", c.Name, c.Binary, err)
		}
	}
}

// TestSecurity_HighAndMediumRiskNeverAutoCommit is the structural guarantee that
// no risky mutation can bypass the stage→execute confirmation gate. The registry
// validator already enforces this at load time; asserting it here over the
// shipped manifests makes the invariant fail loudly if that validator is ever
// weakened, and documents it as a security property rather than an
// implementation detail.
func TestSecurity_HighAndMediumRiskNeverAutoCommit(t *testing.T) {
	for _, c := range loadRegistry(t).All() {
		if c.AutoCommit && (c.Risk == RiskHigh || c.Risk == RiskMedium) {
			t.Errorf("capability %q is auto_commit with risk %q: medium/high-risk operations MUST keep the human-approval gate", c.Name, c.Risk)
		}
	}
}

// TestSecurity_DangerousOpsAreGated pins the metadata of the irrecoverable
// operations: each must exist, be classified high-risk and irreversible, and —
// crucially — never auto-commit, so committing one always requires an explicit
// token from a human-approved step.
func TestSecurity_DangerousOpsAreGated(t *testing.T) {
	reg := loadRegistry(t)
	for _, name := range dangerousOps {
		c, ok := reg.Lookup(name)
		if !ok {
			t.Errorf("expected dangerous op %q to exist in the registry", name)
			continue
		}
		if c.Risk != RiskHigh {
			t.Errorf("%q: risk = %q, want %q (irrecoverable operations must be high-risk)", name, c.Risk, RiskHigh)
		}
		if c.Reversibility != Irreversible {
			t.Errorf("%q: reversibility = %q, want %q", name, c.Reversibility, Irreversible)
		}
		if c.AutoCommit {
			t.Errorf("%q: auto_commit must be false — an irrecoverable op may never fire without explicit confirmation", name)
		}
	}
}

// TestSecurity_TerminateProcessTakesNoSignalParam guards the one capability
// backed by `kill`. Its mutator hardcodes SIGTERM (never SIGKILL) so a process
// always gets a chance to clean up; this test ensures the manifest never grows a
// parameter that could let the caller choose the signal, which would reopen the
// door to a force-kill.
func TestSecurity_TerminateProcessTakesNoSignalParam(t *testing.T) {
	c, ok := loadRegistry(t).Lookup("terminate_process")
	if !ok {
		t.Fatal("terminate_process capability not found")
	}
	// Assert the EXACT shape, not just "no bad param": the contract is a single
	// pid integer. A loop alone would pass vacuously if the params were ever
	// emptied, so pin the count and the one param explicitly.
	if len(c.Params) != 1 {
		t.Fatalf("terminate_process must take exactly one parameter (pid), got %d: %+v", len(c.Params), c.Params)
	}
	p := c.Params[0]
	if p.Name != "pid" {
		t.Errorf("terminate_process parameter is %q, want \"pid\" (never a signal selector)", p.Name)
	}
	if p.Type != TypeInt {
		t.Errorf("terminate_process parameter %q has type %q, want int", p.Name, p.Type)
	}
}
