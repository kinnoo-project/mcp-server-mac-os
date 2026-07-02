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

// TestLoadCases_LayerBFieldsRoundTrip confirms the new optional fields
// (setup/teardown/manual and the new expectation fields) decode from JSON, so a
// corpus using them loads without error.
func TestLoadCases_LayerBFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeCaseFile(t, dir, "b.json", `[
		{
			"id": "move_into_existing_dir",
			"manual": false,
			"setup": {"scratch": "screenshots", "files": ["a.png", "b.png", "archive/.keep"]},
			"teardown": {"remove_scratch": true},
			"turns": [
				{"prompt": "move every png in {{scratch}} into {{scratch}}/archive",
				 "expect": {"tool": "filesystem", "operation": "move", "forbid_tools": ["execute"]}},
				{"prompt": "yes go ahead",
				 "expect": {"tool": "execute", "tool_succeeds": true,
				            "state": {"exists": ["{{scratch}}/archive/a.png"], "absent": ["{{scratch}}/a.png"], "is_dir": ["{{scratch}}/archive"]}}}
			]
		},
		{"id": "manual_case", "manual": true, "prompt": "open TextEdit", "expect": {"tool": "application", "operation": "open_application"}}
	]`)

	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("LoadCases: %v", err)
	}
	// Sorted by ID: "manual_case" < "move_into_existing_dir".
	manual := cases[0]
	if !manual.Manual {
		t.Errorf("manual_case should have Manual=true, got %+v", manual)
	}
	mv := cases[1]
	if mv.Setup == nil || mv.Setup.Scratch != "screenshots" || len(mv.Setup.Files) != 3 {
		t.Fatalf("setup did not decode: %+v", mv.Setup)
	}
	if mv.Teardown == nil || !mv.Teardown.RemoveScratch {
		t.Fatalf("teardown did not decode: %+v", mv.Teardown)
	}
	turns, _ := mv.ResolvedTurns()
	exec := turns[1].Expect
	if exec.ToolSucceeds == nil || !*exec.ToolSucceeds {
		t.Errorf("tool_succeeds did not decode as true: %+v", exec.ToolSucceeds)
	}
	if exec.State == nil || len(exec.State.Exists) != 1 || len(exec.State.Absent) != 1 || len(exec.State.IsDir) != 1 {
		t.Fatalf("state did not decode: %+v", exec.State)
	}
}

// TestApplySetup_CreatesAndConfines confirms fixtures are created inside the
// scratch tree and that the scratch-name guardrail rejects an escaping name.
func TestApplySetup_CreatesAndConfines(t *testing.T) {
	scratch, err := applySetup(&Setup{Scratch: "shots", Files: []string{"a.png", "nested/b.png"}}, "deadbeef")
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	defer os.RemoveAll(scratch)
	for _, rel := range []string{"a.png", "nested/b.png"} {
		if _, err := os.Stat(filepath.Join(scratch, rel)); err != nil {
			t.Errorf("expected fixture %q to exist: %v", rel, err)
		}
	}

	// A bad scratch name (separator / traversal / absolute) must be rejected
	// before anything is created.
	for _, bad := range []string{"", "..", "a/b", "/etc"} {
		if _, err := applySetup(&Setup{Scratch: bad}, "deadbeef"); err == nil {
			t.Errorf("expected applySetup to reject scratch name %q", bad)
		}
	}
	// A file entry escaping via ".." must be rejected too — and the partially
	// created scratch tree must be cleaned up, with "" returned, so a setup error
	// leaves no residue on disk.
	bad, err := applySetup(&Setup{Scratch: "escapecase", Files: []string{"../escape.txt"}}, "deadbeef")
	if err == nil {
		t.Error("expected applySetup to reject a fixture file escaping the scratch dir")
	}
	if bad != "" {
		t.Errorf("expected empty scratch path on error, got %q", bad)
	}
	leftover := filepath.Join(os.TempDir(), "mcp-eval-deadbeef-escapecase")
	if _, statErr := os.Stat(leftover); statErr == nil {
		os.RemoveAll(leftover)
		t.Errorf("expected scratch dir %q to be removed after a setup error", leftover)
	}
}
