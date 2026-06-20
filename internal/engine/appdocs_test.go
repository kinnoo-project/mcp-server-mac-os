// appdocs_test.go covers the pure, subprocess-free halves of the app
// document-type detection used by open_file: the support-matching rule, the
// Info.plist (plutil JSON) parser, and the mdimport type parser. The functions
// that actually shell out (resolveAppBundle/appDeclaredDocTypes/fileUTI) are left
// to the manual smoke test, exactly as the other live-probe mutators are; here we
// pin down the logic those probes feed into.
package engine

import (
	"reflect"
	"testing"
)

// previewDocTypesJSON is a trimmed but realistic plutil JSON rendering of an app
// Info.plist, modelled on Preview: it declares UTIs (no extensions) and includes
// an entry with an empty type list, which must be ignored.
const previewDocTypesJSON = `{
  "CFBundleName": "Preview",
  "CFBundleDocumentTypes": [
    { "LSItemContentTypes": ["public.png", "public.jpeg", "com.adobe.pdf"] },
    { "LSItemContentTypes": ["com.compuserve.gif"] },
    { "CFBundleTypeName": "empty" }
  ]
}`

// editorDocTypesJSON models a text editor that declares plain extensions
// (including a "*" wildcard meaning "any file") alongside a UTI.
const editorDocTypesJSON = `{
  "CFBundleDocumentTypes": [
    { "CFBundleTypeExtensions": ["txt", "MD", "go"], "LSItemContentTypes": ["public.plain-text"] },
    { "CFBundleTypeExtensions": ["*"] }
  ]
}`

func TestParseDocTypes_Preview(t *testing.T) {
	exts, utis, declaredAny, err := parseDocTypes([]byte(previewDocTypesJSON))
	if err != nil {
		t.Fatalf("parseDocTypes: %v", err)
	}
	if !declaredAny {
		t.Error("Preview declares document types; declaredAny should be true")
	}
	if len(exts) != 0 {
		t.Errorf("Preview declares no extensions, got %v", exts)
	}
	for _, want := range []string{"public.png", "public.jpeg", "com.adobe.pdf", "com.compuserve.gif"} {
		if !utis[want] {
			t.Errorf("expected UTI %q to be parsed", want)
		}
	}
}

func TestParseDocTypes_EditorLowercasesAndWildcard(t *testing.T) {
	exts, utis, declaredAny, err := parseDocTypes([]byte(editorDocTypesJSON))
	if err != nil {
		t.Fatalf("parseDocTypes: %v", err)
	}
	if !declaredAny {
		t.Error("declaredAny should be true")
	}
	// Extensions are lowercased so matching is case-insensitive.
	for _, want := range []string{"txt", "md", "go", "*"} {
		if !exts[want] {
			t.Errorf("expected extension %q (lowercased) to be parsed; got %v", want, exts)
		}
	}
	if !utis["public.plain-text"] {
		t.Error("expected the editor's UTI to be parsed")
	}
}

func TestParseDocTypes_NoDeclarations(t *testing.T) {
	_, _, declaredAny, err := parseDocTypes([]byte(`{"CFBundleName":"Whatever"}`))
	if err != nil {
		t.Fatalf("parseDocTypes: %v", err)
	}
	if declaredAny {
		t.Error("an app with no CFBundleDocumentTypes must report declaredAny=false")
	}
}

// TestParseDocTypes_EmptyEntriesAreNoClaim covers the Copilot-flagged case: an app
// can carry CFBundleDocumentTypes entries that declare neither extensions nor UTIs
// (e.g. only a CFBundleTypeName). That is effectively no claim about file types, so
// declaredAny must be false — otherwise assessFileSupport would treat the app as a
// confident "unsupported" mismatch rather than "uncertain".
func TestParseDocTypes_EmptyEntriesAreNoClaim(t *testing.T) {
	const blob = `{
	  "CFBundleDocumentTypes": [
	    { "CFBundleTypeName": "Some Document", "LSHandlerRank": "Owner" },
	    { "CFBundleTypeExtensions": [""], "LSItemContentTypes": [] }
	  ]
	}`
	exts, utis, declaredAny, err := parseDocTypes([]byte(blob))
	if err != nil {
		t.Fatalf("parseDocTypes: %v", err)
	}
	if len(exts) != 0 || len(utis) != 0 {
		t.Errorf("no extensions/UTIs should be extracted; got exts=%v utis=%v", exts, utis)
	}
	if declaredAny {
		t.Error("entries with no extensions/UTIs are no claim at all: declaredAny must be false")
	}
}

func TestAppSupportsFile(t *testing.T) {
	preview, previewUTIs, _, _ := parseDocTypes([]byte(previewDocTypesJSON))
	editor, editorUTIs, _, _ := parseDocTypes([]byte(editorDocTypesJSON))

	cases := []struct {
		name             string
		exts, utis       map[string]bool
		fileExt, fileUTI string
		want             bool
	}{
		{"png by UTI in Preview", preview, previewUTIs, "png", "public.png", true},
		{"pdf by UTI in Preview", preview, previewUTIs, "pdf", "com.adobe.pdf", true},
		{"txt NOT in Preview", preview, previewUTIs, "txt", "public.plain-text", false},
		{"txt by extension in editor", editor, editorUTIs, "txt", "public.plain-text", true},
		{"md by extension (case-insensitive)", editor, editorUTIs, "md", "", true},
		{"wildcard accepts unknown ext", editor, editorUTIs, "xyz", "", true},
		{"no ext and unknown UTI in Preview", preview, previewUTIs, "", "", false},
	}
	for _, c := range cases {
		if got := appSupportsFile(c.exts, c.utis, c.fileExt, c.fileUTI); got != c.want {
			t.Errorf("%s: appSupportsFile = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseMdimportType(t *testing.T) {
	const out = `2026-06-19 ... Imported '/tmp/sample.png' of type 'public.png' with plugIn /System/Library/Spotlight/Image.mdimporter.`
	if uti, ok := parseMdimportType(out); !ok || uti != "public.png" {
		t.Errorf("parseMdimportType = %q, %v; want public.png, true", uti, ok)
	}
	// No type line → not ok.
	if _, ok := parseMdimportType("nothing useful here"); ok {
		t.Error("parseMdimportType should report ok=false when no type is present")
	}
}

func TestFileTypeLabel(t *testing.T) {
	if got := fileTypeLabel("public.png", "png"); got != "public.png" {
		t.Errorf("UTI should win: got %q", got)
	}
	if got := fileTypeLabel("", "txt"); got != ".txt" {
		t.Errorf("extension fallback: got %q", got)
	}
	if got := fileTypeLabel("", ""); got != "an unknown type" {
		t.Errorf("generic fallback: got %q", got)
	}
}

func TestSampleTypes_DeterministicAndCapped(t *testing.T) {
	exts := map[string]bool{"png": true, "jpg": true}
	utis := map[string]bool{"public.png": true}
	got := sampleTypes(exts, utis)
	want := []string{".jpg", ".png", "public.png"} // extensions first, each group sorted
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sampleTypes = %v, want %v", got, want)
	}
}
