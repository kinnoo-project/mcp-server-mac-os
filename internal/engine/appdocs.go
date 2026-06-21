// appdocs.go answers one question the open_file mutator needs before it stages a
// launch: "does THIS application actually know how to open THIS file?"
//
// macOS records that answer declaratively. Every application bundle ships an
// Info.plist whose CFBundleDocumentTypes array lists the document types it opens
// — as filename extensions (CFBundleTypeExtensions) and/or as Uniform Type
// Identifiers (LSItemContentTypes, e.g. "public.png"). A file, in turn, has a
// type the system can compute. Matching the two tells us whether the app handles
// the file.
//
// All of the probing here is READ-ONLY (it stages nothing) and deliberately
// Spotlight-independent: the file's type is read with `mdimport` rather than
// `mdls`, because `mdls` fails on files the Spotlight index has not yet seen,
// whereas `mdimport` computes the type on demand. Like the rest of the engine, it
// composes trusted system binaries through the policy layer and keeps untrusted
// text out of any command that lacks an option terminator.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mcp-server-mac-os/internal/policy"
)

// supportLevel is the three-way verdict of comparing a file's type against an
// application's declared document types. It is three-way rather than boolean
// because "we could not tell" is a distinct, common outcome (an app may declare
// no document types at all, or the file's type may be unreadable) that the caller
// surfaces differently from a confident "no".
type supportLevel int

const (
	// supportSupported: the file's extension or type matches one the app declares.
	supportSupported supportLevel = iota
	// supportUnsupported: we read both sides and found no match — a confident "no".
	supportUnsupported
	// supportUncertain: we could not determine support (app not located, app
	// declares nothing, or the file's type could not be read).
	supportUncertain
)

// fileSupport is the full result of assessFileSupport: the verdict plus the
// human-readable scraps the caller needs to compose a clear preview/warning.
type fileSupport struct {
	// Level is the three-way verdict.
	Level supportLevel
	// Reason is a short clause explaining an Uncertain verdict (e.g. "the app
	// does not declare which file types it opens"); empty for the other levels.
	Reason string
	// FileType is the best label we have for the file's type — its UTI when known
	// (e.g. "public.plain-text"), otherwise its extension (e.g. ".txt").
	FileType string
	// Accepts is a small sample of the types/extensions the app DOES declare,
	// shown in an "unsupported" warning so the user understands what would work.
	Accepts []string
}

// assessFileSupport decides whether app can open file, performing all the
// read-only probing (bundle lookup, Info.plist parse, file-type detection) and
// collapsing it into a single fileSupport verdict. It never returns an error:
// any probing failure becomes a supportUncertain verdict with an explanatory
// reason, because the caller's job is to inform a confirmation prompt, not to
// block — so "could not tell" must be a value, not a failure.
func assessFileSupport(ctx context.Context, app, file string) fileSupport {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(file), "."))

	bundle, ok := resolveAppBundle(ctx, app)
	if !ok {
		return fileSupport{Level: supportUncertain, FileType: fileTypeLabel("", ext),
			Reason: fmt.Sprintf("could not locate %q to check its supported types", app)}
	}

	exts, utis, declaredAny, err := appDeclaredDocTypes(ctx, bundle)
	if err != nil {
		return fileSupport{Level: supportUncertain, FileType: fileTypeLabel("", ext),
			Reason: "could not read the application's list of supported types"}
	}
	if !declaredAny {
		return fileSupport{Level: supportUncertain, FileType: fileTypeLabel("", ext),
			Reason: "the application does not declare which file types it opens"}
	}

	uti, _ := fileUTI(ctx, file)
	label := fileTypeLabel(uti, ext)

	if appSupportsFile(exts, utis, ext, uti) {
		return fileSupport{Level: supportSupported, FileType: label}
	}
	// No match. If we never learned the file's type we cannot be sure it is
	// genuinely unsupported (a known type might have matched), so stay uncertain.
	if uti == "" {
		return fileSupport{Level: supportUncertain, FileType: label,
			Reason: "could not determine the file's type"}
	}
	return fileSupport{Level: supportUnsupported, FileType: label, Accepts: sampleTypes(exts, utis)}
}

// appSupportsFile is the pure matching core, split out so the support rule is
// unit-testable without any subprocess. An app supports a file when it declares
// the file's extension (or the "*" wildcard, meaning "any file"), OR declares the
// file's exact UTI. The exact-UTI rule is intentionally strict — it does not walk
// the UTI conformance tree — because a strict result only adds a warning to a
// preview the user can still confirm; it never blocks the open.
func appSupportsFile(exts, utis map[string]bool, fileExt, fileUTI string) bool {
	if exts["*"] {
		return true
	}
	if fileExt != "" && exts[fileExt] {
		return true
	}
	if fileUTI != "" && utis[fileUTI] {
		return true
	}
	return false
}

// appBundlePrefDirs ranks where a matching app bundle may live, most-preferred
// first, so a user-installed copy in ~/Applications or /Applications wins over a
// system one when several bundles share a name.
func appBundlePrefRank(path string) int {
	home, _ := os.UserHomeDir()
	switch {
	case home != "" && strings.HasPrefix(path, filepath.Join(home, "Applications")+"/"):
		return 0
	case strings.HasPrefix(path, "/Applications/"):
		return 1
	default:
		return 2
	}
}

// resolveAppBundle maps an application name (e.g. "Preview") to its .app bundle
// path. It runs the SAME fixed app-bundle Spotlight query the listing builtins
// use (appBundleQuery in builtins_apps.go) and matches the name in Go, so the
// model-supplied name never enters the mdfind query string — mdfind has no "--"
// terminator, so keeping untrusted text out of it is the only safe option.
//
// Matching is by bundle basename minus ".app", case-insensitively, mirroring how
// `open -a` resolves a name. ok is false when no bundle matches; the caller treats
// that as "uncertain" rather than an error.
func resolveAppBundle(ctx context.Context, name string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, ".app")))
	if want == "" {
		return "", false
	}
	mdfindBin, err := policy.ResolveBinary("mdfind")
	if err != nil {
		return "", false
	}
	res, err := runCommand(ctx, mdfindBin, appBundleQuery)
	if err != nil || res.ExitCode != 0 {
		return "", false
	}

	best, bestRank := "", 99
	for _, p := range splitNonEmptyLines(res.Stdout) {
		base := strings.TrimSuffix(filepath.Base(p), ".app")
		if !strings.EqualFold(base, want) {
			continue
		}
		if r := appBundlePrefRank(p); r < bestRank {
			best, bestRank = p, r
		}
	}
	return best, best != ""
}

// infoPlistDocTypes is the minimal slice of an app's Info.plist we care about:
// the document-type declarations and, within each, the file extensions and UTIs
// it opens.
type infoPlistDocTypes struct {
	CFBundleDocumentTypes []struct {
		CFBundleTypeExtensions []string `json:"CFBundleTypeExtensions"`
		LSItemContentTypes     []string `json:"LSItemContentTypes"`
	} `json:"CFBundleDocumentTypes"`
}

// appDeclaredDocTypes reads an app bundle's Info.plist and returns the set of
// file extensions (lowercased) and UTIs it declares it can open, plus whether it
// declared ANY document types at all. The plist is converted to JSON with
// `plutil` and parsed in Go. The path is passed after "--" so a crafted bundle
// path can never be read as a plutil option.
//
// declaredAny lets the caller distinguish "app opens nothing we recognise" from
// "app makes no claim about file types" — the latter is reported as uncertain
// rather than unsupported. It is true only when at least one extension or UTI was
// actually extracted: an app can carry CFBundleDocumentTypes entries that name no
// extensions and no UTIs (e.g. only a CFBundleTypeName), which is effectively no
// claim at all and must not be mistaken for a confident "unsupported" mismatch.
func appDeclaredDocTypes(ctx context.Context, bundlePath string) (exts, utis map[string]bool, declaredAny bool, err error) {
	plutilBin, rerr := policy.ResolveBinary("plutil")
	if rerr != nil {
		return nil, nil, false, rerr
	}
	plist := filepath.Join(bundlePath, "Contents", "Info.plist")
	res, rerr := runCommand(ctx, plutilBin, "-convert", "json", "-o", "-", "--", plist)
	if rerr != nil {
		return nil, nil, false, rerr
	}
	if res.ExitCode != 0 {
		return nil, nil, false, fmt.Errorf("plutil exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return parseDocTypes([]byte(res.Stdout))
}

// parseDocTypes is the pure parsing half of appDeclaredDocTypes, separated so the
// extraction/normalization can be unit-tested against a captured Info.plist JSON
// blob with no plutil call.
func parseDocTypes(jsonBlob []byte) (exts, utis map[string]bool, declaredAny bool, err error) {
	var info infoPlistDocTypes
	if uerr := json.Unmarshal(jsonBlob, &info); uerr != nil {
		return nil, nil, false, uerr
	}
	exts = make(map[string]bool)
	utis = make(map[string]bool)
	for _, dt := range info.CFBundleDocumentTypes {
		for _, e := range dt.CFBundleTypeExtensions {
			if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
				exts[e] = true
			}
		}
		for _, u := range dt.LSItemContentTypes {
			if u = strings.TrimSpace(u); u != "" {
				utis[u] = true
			}
		}
	}
	return exts, utis, len(exts) > 0 || len(utis) > 0, nil
}

// mdimportTypeRe extracts the UTI from `mdimport -t -d1` debug output, whose line
// reads e.g. `Imported '/path' of type 'public.png' with plugIn ...`.
var mdimportTypeRe = regexp.MustCompile(`of type '([^']*)'`)

// fileUTI computes a file's Uniform Type Identifier using `mdimport` in test mode
// (-t never writes the Spotlight index; -d1 prints the type it inferred). This is
// used in preference to `mdls` because it works even when the file has not been
// Spotlight-indexed. The path is passed after "--" so it can never be parsed as an
// mdimport option. ok is false when no type could be parsed.
//
// mdimport prints its debug line to stderr, so both streams are searched.
func fileUTI(ctx context.Context, file string) (string, bool) {
	mdimportBin, err := policy.ResolveBinary("mdimport")
	if err != nil {
		return "", false
	}
	res, err := runCommand(ctx, mdimportBin, "-t", "-d1", "--", file)
	if err != nil {
		return "", false
	}
	return parseMdimportType(res.Stdout + "\n" + res.Stderr)
}

// parseMdimportType is the pure parsing half of fileUTI, separated for unit
// testing against captured mdimport output.
func parseMdimportType(output string) (string, bool) {
	m := mdimportTypeRe.FindStringSubmatch(output)
	if len(m) < 2 || strings.TrimSpace(m[1]) == "" {
		return "", false
	}
	return m[1], true
}

// fileTypeLabel renders the best available human label for a file's type: its
// UTI when known, otherwise a ".ext" suffix, otherwise a generic phrase.
func fileTypeLabel(uti, ext string) string {
	switch {
	case uti != "":
		return uti
	case ext != "":
		return "." + ext
	default:
		return "an unknown type"
	}
}

// sampleTypes returns up to a handful of the types an app declares, preferring
// readable extensions over UTIs, for inclusion in an "unsupported" warning. The
// result is sorted only enough to be deterministic for tests.
func sampleTypes(exts, utis map[string]bool) []string {
	const maxSamples = 6
	out := make([]string, 0, maxSamples)
	add := func(s string) {
		if len(out) < maxSamples {
			out = append(out, s)
		}
	}
	for _, e := range sortedKeys(exts) {
		add("." + e)
	}
	for _, u := range sortedKeys(utis) {
		add(u)
	}
	return out
}

// sortedKeys returns the keys of a set in deterministic ascending order.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
