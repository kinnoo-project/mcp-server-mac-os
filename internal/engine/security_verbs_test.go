// security_verbs_test.go is a production security gate: it pins the exact verb
// each constrained system binary may be invoked with.
//
// Binaries the registry deny list would otherwise block — codesign, spctl,
// csrutil, xattr, and the keychain side of security (V5/V6), plus tmutil,
// diskutil, and hdiutil (V7) — are reachable through the security and storage
// domains. That is only safe because each is used in a closed set of read-only or
// benign modes and can never reach its state-changing sub-commands (spctl --add,
// csrutil disable, xattr -w, a codesign signing verb, diskutil erase, tmutil
// delete, hdiutil create). This file makes that promise an ENFORCED invariant
// rather than a code-review nicety: it asserts, against the actual argv-builder
// functions in builtins_security.go / builtins_storage.go / mutate_storage.go,
// that every constrained binary's command starts with an allowed verb and
// contains none of the forbidden ones.
//
// It is the reusable frame the storage domain (V7) extended when it un-denied
// tmutil, diskutil, and hdiutil: each got a row in constrainedBinaryVerbs and a
// probe in the argv table below. Extending it the same way is the required step
// whenever a future unit takes another binary off the deny list.
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
	// security (keychain, V6): metadata READS only. The item-search sub-commands
	// find-generic-password / find-internet-password may run, and list-keychains,
	// but never with the secret-printing flags -w (password to stdout) or -g
	// (password to stderr); and the whole-keychain secret dump (dump-keychain, and
	// its -d "dump data" flag) must never appear. This pin is what keeps the
	// keychain domain to "does a saved password EXIST and under what account",
	// never the password value itself.
	"security": {
		allowedFirstArgs: []string{"find-generic-password", "find-internet-password", "list-keychains"},
		forbiddenTokens:  []string{"-w", "-g", "-d", "dump-keychain"},
	},
	// tmutil (storage, V7): Time Machine READS only. status/latestbackup/
	// listbackups/listlocalsnapshots report state; the verbs that CHANGE it —
	// delete a backup, delete local snapshots, restore, start/stop a run,
	// re-point or remove the destination, adopt an inherited backup — must never
	// appear. This pin keeps the Time Machine domain to "what backups exist and is
	// one running", never "delete/restore a backup".
	"tmutil": {
		allowedFirstArgs: []string{"status", "latestbackup", "listbackups", "listlocalsnapshots"},
		forbiddenTokens: []string{
			"delete", "deletelocalsnapshots", "restore", "startbackup", "stopbackup",
			"setdestination", "removedestination", "inheritbackup", "associatedisk", "disable", "enable",
		},
	},
	// diskutil (storage, V7): list/info/mount only. list and info are read-only;
	// mount brings an existing volume online (benign, and staged for confirmation).
	// Every destructive or reformatting verb — erase*, partition*, reformat,
	// repair*, resize, rename, apfs operations, and the unmount/eject verbs
	// (eject_volume only ADVISES, it never runs one) — is forbidden anywhere in
	// argv. ("mount" is allowed; the distinct tokens "unmount"/"unmountDisk"/
	// "mountDisk" are forbidden — exact-token matching keeps them separate.)
	"diskutil": {
		allowedFirstArgs: []string{"list", "info", "mount"},
		forbiddenTokens: []string{
			"erase", "eraseDisk", "eraseVolume", "secureErase", "zeroDisk", "randomDisk",
			"partitionDisk", "reformat", "repairDisk", "verifyDisk", "repairVolume",
			"resizeVolume", "rename", "apfs", "eject", "unmount", "unmountDisk", "mountDisk",
		},
	},
	// hdiutil (storage, V7): attach/detach/imageinfo only. Attaching and detaching
	// a disk image are benign and reversible-by-hand; the verbs that CREATE or
	// rewrite images — create, convert, resize, burn, makehybrid, compact,
	// erasekeys, chpass — must never appear.
	"hdiutil": {
		allowedFirstArgs: []string{"attach", "detach", "imageinfo"},
		forbiddenTokens:  []string{"create", "convert", "resize", "burn", "makehybrid", "compact", "erasekeys", "chpass"},
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
		// Keychain metadata reads (V6): both find-* forms and the list form, none
		// of which may ever carry -w/-g/-d/dump-keychain. Sample service/account
		// values are benign data, irrelevant to what the verb pin proves.
		{"find_credential", "security", findGenericPasswordArgs("SampleService", "sample@example.com")},
		{"find_internet_credential", "security", findInternetPasswordArgs("example.com", "sample")},
		{"list_keychains", "security", listKeychainsArgs()},
		// Storage domain (V7). tmutil reads take no input; diskutil/hdiutil
		// argv builders take a benign sample identifier/path — the verb pin is
		// about argv[0] and the forbidden tokens, so the operand value is
		// irrelevant to what this proves.
		{"time_machine_status", "tmutil", tmutilStatusArgs()},
		{"time_machine_status/latest", "tmutil", tmutilLatestBackupArgs()},
		{"list_backups", "tmutil", tmutilListBackupsArgs()},
		{"list_backups/snapshots", "tmutil", tmutilListSnapshotsArgs()},
		{"list_volumes", "diskutil", diskutilListArgs()},
		{"volume_info", "diskutil", diskutilInfoArgs("disk2s1")},
		{"mount_volume", "diskutil", diskutilMountArgs("disk2s1")},
		{"attach_disk_image", "hdiutil", hdiutilAttachArgs("/tmp/image.dmg")},
		{"detach_disk_image", "hdiutil", hdiutilDetachArgs("/Volumes/Installer")},
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
