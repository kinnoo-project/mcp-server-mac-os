// builtins_keychain_test.go covers the security domain's keychain-metadata
// builtins (V6). Its center of gravity is the one property that matters most:
// a saved password's VALUE can never appear in the output. That is proved from
// two directions —
//
//   - the argv builders never emit the secret-printing flags -w/-g (also pinned
//     by security_verbs_test.go), and
//   - the attribute parser keychainMetadata forwards ONLY an allowlist of
//     non-secret fields, dropping anything unrecognized — verified here against a
//     canned dump that deliberately includes a secret-looking blob.
//
// The free-text service/account inputs also get the CLAUDE.md §4 dash-leading
// regression. A live path that shells out to the real `security` is gated behind
// MCP_SECURITY_LIVE=1 because its output depends on host keychain contents.
package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestKeychainArgvBuilders pins the exact argument vectors, most importantly the
// ABSENCE of -w/-g: a future edit that added a secret-printing flag (or dropped a
// query flag) fails here in addition to TestSecurity_ConstrainedBinaryVerbs.
func TestKeychainArgvBuilders(t *testing.T) {
	if got, want := strings.Join(findGenericPasswordArgs("Svc", "acct"), " "), "find-generic-password -s Svc -a acct"; got != want {
		t.Errorf("findGenericPasswordArgs = %q, want %q", got, want)
	}
	// Only one field supplied → only that flag appears.
	if got, want := strings.Join(findGenericPasswordArgs("Svc", ""), " "), "find-generic-password -s Svc"; got != want {
		t.Errorf("findGenericPasswordArgs(service only) = %q, want %q", got, want)
	}
	if got, want := strings.Join(findInternetPasswordArgs("example.com", ""), " "), "find-internet-password -s example.com"; got != want {
		t.Errorf("findInternetPasswordArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(listKeychainsArgs(), " "), "list-keychains"; got != want {
		t.Errorf("listKeychainsArgs = %q, want %q", got, want)
	}
	// Belt-and-braces: no argv builder may ever emit a secret-printing flag.
	for _, argv := range [][]string{
		findGenericPasswordArgs("Svc", "acct"),
		findInternetPasswordArgs("example.com", "acct"),
		listKeychainsArgs(),
	} {
		for _, a := range argv {
			if a == "-w" || a == "-g" {
				t.Errorf("argv %v contains a secret-printing flag %q — keychain reads must be metadata-only", argv, a)
			}
		}
	}
}

// TestKeychain_HostileQueryRejected is the per-operation injection regression: a
// dash-leading service/account value must be refused by validateKeychainQuery
// before any subprocess runs, so it can never be mistaken for a flag.
func TestKeychain_HostileQueryRejected(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	for _, hostile := range []string{"-w", "-g", "-e", "--flood", "-"} {
		// find_credential via the service field.
		if _, err := runFindCredential(ctx, cap, map[string]any{"service": hostile}); err == nil ||
			!strings.Contains(err.Error(), "'-'") {
			t.Errorf("find_credential(service=%q): expected a dash-guard error, got %v", hostile, err)
		}
		// find_internet_credential via the account field.
		if _, err := runFindInternetCredential(ctx, cap, map[string]any{"account": hostile}); err == nil ||
			!strings.Contains(err.Error(), "'-'") {
			t.Errorf("find_internet_credential(account=%q): expected a dash-guard error, got %v", hostile, err)
		}
	}
	// A control character must also be refused.
	if _, err := runFindCredential(ctx, cap, map[string]any{"service": "a\nb"}); err == nil ||
		!strings.Contains(err.Error(), "control character") {
		t.Errorf("find_credential with a newline: expected a control-character error, got %v", err)
	}
}

// TestKeychain_RequiresAtLeastOneField pins the "give service or account"
// contract: an empty query would otherwise match an arbitrary first keychain item.
func TestKeychain_RequiresAtLeastOneField(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	if _, err := runFindCredential(ctx, cap, map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "at least one") {
		t.Errorf("find_credential with no fields: expected an 'at least one' error, got %v", err)
	}
	if _, err := runFindInternetCredential(ctx, cap, map[string]any{"server": "  "}); err == nil ||
		!strings.Contains(err.Error(), "at least one") {
		t.Errorf("find_internet_credential with only blank fields: expected an 'at least one' error, got %v", err)
	}
}

// TestKeychainMetadata_SecretSafeAllowlist is the security heart of V6: the parser
// must surface the reviewed non-secret fields and DROP everything else — including
// an attribute whose value looks like a secret. If a future change accidentally
// widened the allowlist to a secret-bearing key, this fails.
func TestKeychainMetadata_SecretSafeAllowlist(t *testing.T) {
	// A realistic attribute dump. "gena" carries a secret-looking blob that must
	// NOT be forwarded; the password itself never appears in this dump form.
	dump := `keychain: "/Users/x/Library/Keychains/login.keychain-db"
version: 512
class: "genp"
attributes:
    0x00000007 <blob>="My Wi-Fi"
    0x00000008 <blob>=<NULL>
    "acct"<blob>="jane@example.com"
    "cdat"<timedate>=0x32303235313132395A00  "20251129232947Z\000"
    "desc"<blob>="AirPort network password"
    "gena"<blob>="S3cr3tShouldNeverLeak"
    "icmt"<blob>="private comment"
    "mdat"<timedate>=0x32303235313230315A00  "20251201090000Z\000"
    "svce"<blob>="AirPort"
    "type"<uint32>=<NULL>`

	out := keychainMetadata(dump)

	// Allowlisted fields present.
	for _, want := range []string{
		"Label: My Wi-Fi",
		"Service: AirPort",
		"Account: jane@example.com",
		"Kind: AirPort network password",
		"Modified: 20251201090000Z",
		"Created: 20251129232947Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("keychainMetadata missing %q; got:\n%s", want, out)
		}
	}
	// The secret-looking blob and undisclosed attributes must be dropped.
	for _, forbidden := range []string{"S3cr3tShouldNeverLeak", "gena", "private comment", "icmt", "\\000"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("keychainMetadata leaked disallowed content %q; got:\n%s", forbidden, out)
		}
	}
}

// TestInterpretCredentialResult pins the three-way branching a keychain lookup
// depends on (Copilot review, PR #63): a match (exit 0) is parsed and framed
// secret-safe, a genuine not-found (exit 44 / the "could not be found" message)
// reads as a plain "no such item" answer, but ANY OTHER failure surfaces as an
// error rather than being misreported as not-found. It also guards against the
// "No a saved password" grammar slip by asserting the exact message wording.
func TestInterpretCredentialResult(t *testing.T) {
	const kind = "saved password"

	// Match: exit 0 with an attribute dump → Found + only allowlisted metadata.
	dump := "attributes:\n    \"svce\"<blob>=\"AirPort\"\n    \"acct\"<blob>=\"jane@example.com\""
	if out, err := interpretCredentialResult(kind, &runResult{Stdout: dump, ExitCode: 0}); err != nil {
		t.Errorf("match: unexpected error %v", err)
	} else if !strings.Contains(out, "Found a saved password") || !strings.Contains(out, "Service: AirPort") {
		t.Errorf("match: want a Found header with metadata, got %q", out)
	}

	// Not-found by exit code 44 (errSecItemNotFound): benign, grammatical message.
	out, err := interpretCredentialResult(kind, &runResult{
		Stderr:   "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.",
		ExitCode: 44,
	})
	if err != nil {
		t.Errorf("not-found(44): unexpected error %v", err)
	}
	if want := "No saved password was found matching that search."; out != want {
		t.Errorf("not-found(44): got %q, want exactly %q", out, want)
	}

	// Any other failure (e.g. a permission/interaction error on a non-44 exit) must
	// NOT be reported as not-found — it must surface as an error.
	if out, err := interpretCredentialResult(kind, &runResult{
		Stderr:   "security: interaction not allowed",
		ExitCode: 1,
	}); err == nil {
		t.Errorf("other failure: expected an error, got output %q", out)
	} else if strings.Contains(err.Error(), "was found") {
		t.Errorf("other failure: must not claim not-found, got %v", err)
	}
}

// TestKeychainMetadata_Empty confirms a dump with no allowlisted attributes yields
// an empty string (the caller then reports "no readable metadata") rather than a
// panic or a stray header.
func TestKeychainMetadata_Empty(t *testing.T) {
	if got := keychainMetadata("class: \"genp\"\nattributes:\n    \"type\"<uint32>=<NULL>"); got != "" {
		t.Errorf("keychainMetadata with no allowlisted fields = %q, want empty", got)
	}
}

// TestLastQuoted checks the value extractor used by the parser: it returns the
// content of the LAST quoted pair (the readable value on both blob and timedate
// rows), and fails cleanly on a line with no complete pair.
func TestLastQuoted(t *testing.T) {
	if v, ok := lastQuoted(`"svce"<blob>="AirPort"`); !ok || v != "AirPort" {
		t.Errorf(`lastQuoted blob = (%q,%v), want ("AirPort",true)`, v, ok)
	}
	if v, ok := lastQuoted(`"mdat"<timedate>=0x3230  "20251201090000Z\000"`); !ok || v != `20251201090000Z\000` {
		t.Errorf(`lastQuoted timedate = (%q,%v), want the trailing timestamp`, v, ok)
	}
	if _, ok := lastQuoted(`no quotes here`); ok {
		t.Errorf("lastQuoted on an unquoted line: ok=true, want false")
	}
}

// TestKeychainBuiltins_Live exercises the real `security` binary end-to-end.
// Skipped unless MCP_SECURITY_LIVE=1 because it reads the host keychain search
// list (and may trigger an access prompt). list_keychains is a stable, always-
// present read; the find path is best-effort (there may be no matching item on a
// given machine), but whatever it returns must never contain a secret-print
// framing slip.
func TestKeychainBuiltins_Live(t *testing.T) {
	if os.Getenv("MCP_SECURITY_LIVE") != "1" {
		t.Skip("set MCP_SECURITY_LIVE=1 to run the live keychain builtins")
	}
	ctx := context.Background()
	cap := registry.Capability{}

	out, err := runListKeychains(ctx, cap, nil)
	if err != nil {
		t.Fatalf("runListKeychains: %v", err)
	}
	if !strings.Contains(out, "keychain") {
		t.Errorf("list_keychains missing any keychain path, got: %s", out)
	}

	// A metadata lookup, whichever way it resolves, must be framed as secret-safe
	// and must never print anything under a "password:" label (the -g output shape
	// we never request).
	res, err := runFindCredential(ctx, cap, map[string]any{"service": "AirPort"})
	if err != nil {
		t.Fatalf("runFindCredential: %v", err)
	}
	if strings.Contains(strings.ToLower(res), "password:") {
		t.Errorf("find_credential output contains a 'password:' line — a secret may have leaked:\n%s", res)
	}
}
