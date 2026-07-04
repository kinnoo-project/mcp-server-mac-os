// builtins_keychain.go implements the read-only "keychain metadata" builtins of
// the security domain — the operations that answer "do I have a saved password
// for X, and what account is it under?" WITHOUT ever revealing the password:
//
//   - find_credential          — is there a saved app/service password? (security find-generic-password)
//   - find_internet_credential — is there a saved website/server login?  (security find-internet-password)
//   - list_keychains           — which keychain files are on the search list? (security list-keychains)
//
// # The one non-negotiable property: secrets never leave the keychain
//
// The macOS `security` tool CAN print a stored password — but only when asked
// with -w (print the password to stdout) or -g (print it to stderr). None of the
// argv builders below ever emit those flags; the find-* invocations request only
// the item's attributes, which `security` prints WITHOUT the secret. That pin is
// asserted two ways: security_verbs_test.go treats -w/-g/-d/dump-keychain as
// forbidden tokens in any `security` argv (the same verb-pinning frame V5 built
// for codesign/spctl/csrutil/xattr), and the parser below is a SECOND, independent
// layer of defense — it re-emits only an allowlist of known-non-secret attribute
// keys (service, account, label, dates, …). So even if a future macOS somehow
// surfaced secret-bearing data in the attribute dump, an unrecognized key is
// dropped, not forwarded.
//
// # Injection posture
//
// find_credential / find_internet_credential take model-controlled service /
// account / server strings. Each rides as the VALUE of a flag (`-s <service>`,
// `-a <account>`), so getopt binds it to that flag even if it began with '-' —
// option injection is structurally impossible here. As defense in depth we still
// reject dash-leading and control-character values up front (validateKeychainQuery)
// so a hostile value can never be mistaken for a flag and can never smuggle a
// newline into the report. list_keychains takes no input at all.
package engine

import (
	"context"
	"fmt"
	"strings"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// findGenericPasswordArgs is the pinned argument vector for find_credential.
// `security find-generic-password` is asked for an item's ATTRIBUTES only: the
// secret-printing flags -w and -g are deliberately absent, so the password value
// is never requested. The service and account are supplied as the values of -s
// and -a respectively (a hostile dash-leading value therefore binds to its flag,
// not to a new option). At least one of the two is always present (the caller
// enforces that). Split out as a pure function so the verb-pinning invariant can
// assert the exact argv shape — and the absence of -w/-g — without invoking the
// binary.
func findGenericPasswordArgs(service, account string) []string {
	args := []string{"find-generic-password"}
	if service != "" {
		args = append(args, "-s", service)
	}
	if account != "" {
		args = append(args, "-a", account)
	}
	return args
}

// findInternetPasswordArgs is the pinned argument vector for
// find_internet_credential: the internet-password twin of the above. It asks for
// attributes only (no -w/-g), with the server bound to -s and the account to -a.
func findInternetPasswordArgs(server, account string) []string {
	args := []string{"find-internet-password"}
	if server != "" {
		args = append(args, "-s", server)
	}
	if account != "" {
		args = append(args, "-a", account)
	}
	return args
}

// listKeychainsArgs is the pinned argument vector for list_keychains: a fixed
// invocation with no model input and no injection surface. It never uses the
// -d/-s domain or set-search-list forms, so it can only READ the search list.
func listKeychainsArgs() []string {
	return []string{"list-keychains"}
}

// validateKeychainQuery is the input guard shared by the two find-* operations.
// The value is destined to be the argument of a -s/-a flag, so option injection
// is already impossible; this rejects empty, over-long, dash-leading, and
// control-character values as defense in depth (and so a value can never inject a
// newline into the rendered report). It returns the trimmed value.
func validateKeychainQuery(op, field, raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("%s: %s must not be empty", op, field)
	}
	if len(v) > 256 {
		return "", fmt.Errorf("%s: %s is too long (max 256 characters)", op, field)
	}
	if v[0] == '-' {
		return "", fmt.Errorf("%s: %s %q must not begin with '-' (it would be read as a command-line option)", op, field, v)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%s: %s contains a control character, which is not allowed", op, field)
		}
	}
	return v, nil
}

// runFindCredential reports whether the login keychain holds a generic (app/
// service) password matching the given service and/or account, and if so emits
// ONLY its non-secret metadata. `security` exits non-zero with a "could not be
// found" message when there is no match — the common, benign "no saved password"
// case, reported as such rather than as an error.
func runFindCredential(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	service, account, err := readCredentialQuery("find_credential", in, "service", "account")
	if err != nil {
		return "", err
	}
	return runCredentialLookup(ctx, "saved password", findGenericPasswordArgs(service, account))
}

// runFindInternetCredential is the internet-password twin of runFindCredential:
// same secret-safe contract over `security find-internet-password`, keyed by
// server and/or account.
func runFindInternetCredential(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	server, account, err := readCredentialQuery("find_internet_credential", in, "server", "account")
	if err != nil {
		return "", err
	}
	return runCredentialLookup(ctx, "saved internet login", findInternetPasswordArgs(server, account))
}

// readCredentialQuery reads and validates the two optional query fields shared by
// the find-* operations and enforces the "at least one" contract. primaryField is
// the service/server key; both fields are individually optional but their union
// must be non-empty, otherwise the lookup would match an arbitrary first item.
func readCredentialQuery(op string, in map[string]any, primaryField, accountField string) (primary, account string, err error) {
	if raw, ok := getString(in, primaryField); ok && strings.TrimSpace(raw) != "" {
		if primary, err = validateKeychainQuery(op, primaryField, raw); err != nil {
			return "", "", err
		}
	}
	if raw, ok := getString(in, accountField); ok && strings.TrimSpace(raw) != "" {
		if account, err = validateKeychainQuery(op, accountField, raw); err != nil {
			return "", "", err
		}
	}
	if primary == "" && account == "" {
		return "", "", fmt.Errorf("%s: give at least one of %q or %q to search for", op, primaryField, accountField)
	}
	return primary, account, nil
}

// runCredentialLookup runs a prepared find-* argv and renders the result. It is
// the shared body of both find-* operations: run `security`, then hand the
// outcome to interpretCredentialResult. kind names what was searched for, as the
// bare noun phrase "saved password" / "saved internet login" (the messages supply
// their own "a"/"No").
func runCredentialLookup(ctx context.Context, kind string, args []string) (string, error) {
	bin, err := policy.ResolveBinary("security")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, args...)
	if err != nil {
		return "", err
	}
	return interpretCredentialResult(kind, res)
}

// interpretCredentialResult renders the outcome of a `security find-*-password`
// lookup. It is the security-relevant seam of the find-* operations, so it is a
// pure function with its own tests. Like interpretQuarantineResult, it separates
// THREE cases a naive "non-zero ⇒ not found" check would conflate (Copilot review,
// PR #63):
//
//   - a MATCH (exit 0): parse the attribute dump through keychainMetadata so ONLY
//     allowlisted, non-secret fields are surfaced;
//   - a genuine NOT-FOUND: `security` exits 44 (errSecItemNotFound), or prints its
//     "could not be found in the keychain" message — the common, benign "you have
//     no such saved item" outcome;
//   - ANY OTHER failure — a usage error, a permission/interaction error, an
//     unexpected non-zero exit — which must surface as an ERROR, never be
//     misreported as "not found". Silently calling a failed lookup "not found"
//     could hide a real problem (e.g. access was denied) and mislead the caller.
func interpretCredentialResult(kind string, res *runResult) (string, error) {
	if res.ExitCode == 0 {
		meta := keychainMetadata(res.Stdout)
		if meta == "" {
			// A match with no renderable attributes: still confirm existence
			// without inventing fields.
			return fmt.Sprintf("Found a %s, but it exposes no readable metadata. The password value is never shown.", kind), nil
		}
		return fmt.Sprintf("Found a %s (the password value itself is never shown):\n\n%s", kind, meta), nil
	}
	stderr := strings.TrimSpace(res.Stderr)
	// errSecItemNotFound is exit 44; the message form is matched too as
	// belt-and-braces in case a future macOS reports it differently.
	if res.ExitCode == 44 || strings.Contains(stderr, "could not be found") {
		return fmt.Sprintf("No %s was found matching that search.", kind), nil
	}
	detail := stderr
	if detail == "" {
		detail = fmt.Sprintf("security exited with status %d and produced no detail", res.ExitCode)
	}
	return "", fmt.Errorf("keychain lookup for a %s failed: %s", kind, detail)
}

// runListKeychains lists the keychain files on the search list. Fixed argv, no
// model input, no injection surface, and no secret in the output — only paths.
func runListKeychains(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("security")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, listKeychainsArgs()...)
	if err != nil {
		return "", err
	}
	// Each output line is a quoted path with leading whitespace; normalize to a
	// clean bulleted list.
	var paths []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		p := strings.Trim(strings.TrimSpace(line), "\"")
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return "No keychains are on the search list.", nil
	}
	var b strings.Builder
	b.WriteString("Keychains on the search list:\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "  • %s\n", p)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// keychainAttributeLabels is the ALLOWLIST at the heart of the secret-safe
// contract: it maps the raw four-character keychain attribute codes `security`
// prints to the human labels we are willing to surface. A code that is not in
// this map is dropped, never forwarded — so the report can only ever contain
// fields we have reviewed as non-secret. Deliberately EXCLUDED are catch-all
// blobs such as "gena" (generic attribute) and "icmt"/"crtr" whose contents are
// app-defined and could carry sensitive data; the password itself never appears
// in this dump at all (it requires the -w/-g flags we never pass).
var keychainAttributeLabels = map[string]string{
	"svce": "Service",
	"acct": "Account",
	"srvr": "Server",
	"ptcl": "Protocol",
	"path": "Path",
	"port": "Port",
	"desc": "Kind",
	"cdat": "Created",
	"mdat": "Modified",
}

// keychainMetadata parses the attribute dump from `security find-*-password` and
// re-emits ONLY the allowlisted, non-secret fields (see keychainAttributeLabels),
// plus the item's label (the 0x00000007 "print name" attribute). It is the
// second, independent layer of the secret-safe guarantee and a pure function with
// its own tests. The dump format is one attribute per indented line, either
//
//	"svce"<blob>="AirPort"
//	0x00000007 <blob>="My Wi-Fi"
//	"mdat"<timedate>=0x3230...  "20251129232947Z\000"
//
// so the parser keys off the four-char code (or the 0x7 label code) and takes the
// LAST double-quoted token on the line as the value (which, for timedate rows,
// is the readable timestamp rather than the hex blob).
func keychainMetadata(dump string) string {
	values := map[string]string{}
	var label string
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// The label attribute is printed with the numeric code 0x00000007 rather
		// than a four-char name; capture it separately.
		if strings.HasPrefix(line, "0x00000007 ") {
			if v, ok := lastQuoted(line); ok {
				label = v
			}
			continue
		}
		// Named attributes look like `"code"<type>=value`. Pull the four-char code
		// out of the leading quotes and keep it only if it is on the allowlist.
		if !strings.HasPrefix(line, "\"") {
			continue
		}
		end := strings.Index(line[1:], "\"")
		if end < 0 {
			continue
		}
		code := line[1 : 1+end]
		if _, ok := keychainAttributeLabels[code]; !ok {
			continue // unrecognized/undisclosed attribute: drop it
		}
		if v, ok := lastQuoted(line); ok && v != "" {
			values[code] = decodeKeychainValue(code, v)
		}
	}

	var b strings.Builder
	if label != "" {
		fmt.Fprintf(&b, "Label: %s\n", label)
	}
	// Emit allowlisted fields in a fixed, readable order (not map-iteration order).
	for _, code := range []string{"svce", "acct", "srvr", "ptcl", "port", "path", "desc", "cdat", "mdat"} {
		if v, ok := values[code]; ok {
			fmt.Fprintf(&b, "%s: %s\n", keychainAttributeLabels[code], v)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// decodeKeychainValue tidies a raw attribute value for display. Timedate fields
// (cdat/mdat) print as the compact `YYYYMMDDhhmmssZ` string with a trailing NUL
// escape (`\000`); strip that so the report shows a clean UTC timestamp. Other
// fields are returned as-is.
func decodeKeychainValue(code, raw string) string {
	v := strings.TrimSuffix(raw, "\\000")
	if code == "cdat" || code == "mdat" {
		// e.g. "20251129232947Z" — leave the compact form (it is unambiguous UTC)
		// but trim any stray NUL escape already handled above.
		return v
	}
	return v
}

// lastQuoted returns the content of the LAST double-quoted substring on a line,
// which for keychain attribute rows is the human-readable value (blobs quote the
// value directly; timedate rows put the readable timestamp in the final quotes,
// after the hex blob). ok is false when the line has no complete quoted pair.
func lastQuoted(line string) (string, bool) {
	last := strings.LastIndex(line, "\"")
	if last <= 0 {
		return "", false
	}
	prev := strings.LastIndex(line[:last], "\"")
	if prev < 0 {
		return "", false
	}
	return line[prev+1 : last], true
}
