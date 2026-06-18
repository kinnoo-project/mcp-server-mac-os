// case_test.go exercises case loading and validation against fixture files
// written to a temp directory — no network calls, no API key needed.
package evals

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCaseFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture %s: %v", name, err)
	}
}

// TestLoadCases_SingleAndMultiTurn confirms both the Prompt sugar and explicit
// Turns load correctly and resolve to the expected []Turn shape.
func TestLoadCases_SingleAndMultiTurn(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "a.json", `[
		{"id": "ls_downloads", "prompt": "what's in Downloads?", "expect": {"tool": "filesystem", "operation": "ls"}},
		{"id": "mkdir_then_confirm", "turns": [
			{"prompt": "create a folder demo", "expect": {"tool": "filesystem", "operation": "mkdir", "forbid_tools": ["execute"]}},
			{"prompt": "yes, go ahead", "expect": {"tool": "execute"}}
		]}
	]`)

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(cases))
	}

	// Sorted by ID: "ls_downloads" < "mkdir_then_confirm".
	turns, err := cases[0].ResolvedTurns()
	if err != nil || len(turns) != 1 || turns[0].Expect.Tool != "filesystem" {
		t.Errorf("ls_downloads should resolve to one turn expecting filesystem, got %+v err=%v", turns, err)
	}

	turns, err = cases[1].ResolvedTurns()
	if err != nil || len(turns) != 2 {
		t.Fatalf("mkdir_then_confirm should resolve to 2 turns, got %+v err=%v", turns, err)
	}
	if len(turns[0].Expect.ForbidTools) != 1 || turns[0].Expect.ForbidTools[0] != "execute" {
		t.Errorf("first turn should forbid execute, got %+v", turns[0].Expect)
	}
}

// TestLoadCases_RejectsBothPromptAndTurns confirms a case setting both forms is
// rejected at load time rather than silently picking one.
func TestLoadCases_RejectsBothPromptAndTurns(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "bad.json", `[
		{"id": "ambiguous", "prompt": "x", "turns": [{"prompt": "y"}]}
	]`)
	if _, err := LoadCases(dir); err == nil {
		t.Fatal("expected an error for a case with both prompt and turns")
	}
}

// TestLoadCases_RejectsNeitherPromptNorTurns confirms a case setting neither
// form is rejected.
func TestLoadCases_RejectsNeitherPromptNorTurns(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "bad.json", `[{"id": "empty"}]`)
	if _, err := LoadCases(dir); err == nil {
		t.Fatal("expected an error for a case with neither prompt nor turns")
	}
}

// TestLoadCases_RejectsDuplicateID confirms two cases sharing an ID — even
// across different files — are caught at load time.
func TestLoadCases_RejectsDuplicateID(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "a.json", `[{"id": "dup", "prompt": "x"}]`)
	writeCaseFile(t, dir, "b.json", `[{"id": "dup", "prompt": "y"}]`)
	if _, err := LoadCases(dir); err == nil {
		t.Fatal("expected an error for a duplicate case id across files")
	}
}

// TestLoadCases_SortedDeterministically confirms case order doesn't depend on
// filesystem directory iteration order.
func TestLoadCases_SortedDeterministically(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "z.json", `[{"id": "zzz", "prompt": "x"}]`)
	writeCaseFile(t, dir, "a.json", `[{"id": "aaa", "prompt": "x"}]`)

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	if len(cases) != 2 || cases[0].ID != "aaa" || cases[1].ID != "zzz" {
		t.Fatalf("expected cases sorted [aaa, zzz], got %v", []string{cases[0].ID, cases[1].ID})
	}
}

// TestLoadCases_IgnoresNonJSONFiles confirms a stray non-.json file in the
// cases directory (e.g. a README) is skipped rather than causing a parse error.
func TestLoadCases_IgnoresNonJSONFiles(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "a.json", `[{"id": "ok", "prompt": "x"}]`)
	writeCaseFile(t, dir, "README.md", `not json`)

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("expected the README to be ignored, got %d cases", len(cases))
	}
}
