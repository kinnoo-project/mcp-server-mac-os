// mutate_filesystem_test.go tests the three reversible file mutators — move,
// copy, and remove — covering both the staged plan they produce and a real
// forward→inverse round trip on disk.
//
// remove and copy reverse through the user's Trash, so every test that executes
// those plans first redirects $HOME to a temp directory (os.UserHomeDir reads
// $HOME on Darwin) and creates a .Trash there. This keeps the real Trash
// untouched while still exercising the genuine commands end to end.
package engine

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// runPlanCommand executes one staged Command for real, resolving the binary the
// same way production does. It is how the round-trip tests assert reversibility.
func runPlanCommand(t *testing.T, cmd Command) {
	t.Helper()
	bin, err := policy.ResolveBinary(cmd.Binary)
	if err != nil {
		t.Fatalf("resolving %q: %v", cmd.Binary, err)
	}
	if out, err := exec.Command(bin, cmd.Args...).CombinedOutput(); err != nil {
		t.Fatalf("running %s %v: %v (%s)", cmd.Binary, cmd.Args, err, out)
	}
}

// redirectHomeWithTrash points $HOME at a fresh temp dir containing a .Trash, so
// the Trash-routed mutators recycle into a sandbox rather than the real Trash.
func redirectHomeWithTrash(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".Trash"), 0o755); err != nil {
		t.Fatalf("creating sandbox Trash: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

func TestStageMove_PlanAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "renamed.txt")

	plan, err := stageMove(context.Background(), registry.Capability{}, map[string]any{"source": src, "destination": dst})
	if err != nil {
		t.Fatalf("stageMove: %v", err)
	}
	wantFwd := Command{Binary: "mv", Args: []string{"--", src, dst}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	wantInv := Command{Binary: "mv", Args: []string{"--", dst, src}}
	if plan.Inverse == nil || !reflect.DeepEqual(*plan.Inverse, wantInv) {
		t.Errorf("Inverse = %+v, want %+v", plan.Inverse, wantInv)
	}

	// Forward moves the file; inverse must put it back exactly.
	runPlanCommand(t, plan.Forward)
	if pathExists(src) || !pathExists(dst) {
		t.Fatalf("after forward: src exists=%v dst exists=%v", pathExists(src), pathExists(dst))
	}
	runPlanCommand(t, *plan.Inverse)
	if !pathExists(src) || pathExists(dst) {
		t.Fatalf("after undo: src exists=%v dst exists=%v", pathExists(src), pathExists(dst))
	}
}

// TestStageMove_IntoExistingDir confirms the "move X into folder Y" semantics:
// when the destination is an existing directory, the final path is Y/basename(X)
// — this is the case from the bug report ("move test.txt to the Desktop folder").
func TestStageMove_IntoExistingDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "Desktop")
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := stageMove(context.Background(), registry.Capability{}, map[string]any{"source": src, "destination": destDir})
	if err != nil {
		t.Fatalf("stageMove: %v", err)
	}
	wantFinal := filepath.Join(destDir, "test.txt")
	if plan.Forward.Args[len(plan.Forward.Args)-1] != wantFinal {
		t.Errorf("final destination = %q, want %q", plan.Forward.Args[len(plan.Forward.Args)-1], wantFinal)
	}
}

func TestStageMove_Rejects(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "a.txt")
	occupied := filepath.Join(dir, "b.txt")
	for _, p := range []string{existing, occupied} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := map[string]map[string]any{
		"missing source":      {"destination": filepath.Join(dir, "new.txt")},
		"missing destination": {"source": existing},
		"nonexistent source":  {"source": filepath.Join(dir, "ghost.txt"), "destination": filepath.Join(dir, "new.txt")},
		"overwrite refused":   {"source": existing, "destination": occupied},
		"dash source":         {"source": "-rf", "destination": filepath.Join(dir, "new.txt")},
		"dash destination":    {"source": existing, "destination": "-x"},
		// mv/cp do not create intermediate directories, so a destination whose
		// parent does not exist must be rejected at stage time (not surface later
		// as a failed forward command).
		"missing dest parent": {"source": existing, "destination": filepath.Join(dir, "no_such_dir", "new.txt")},
		// A destination parent that exists but is a regular file (not a directory)
		// is equally doomed and must be rejected.
		"dest parent not dir": {"source": existing, "destination": filepath.Join(occupied, "new.txt")},
	}
	for name, in := range cases {
		if _, err := stageMove(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestStageMoveGlob_PlanAndRoundTrip exercises the batch (source_glob) move: many
// files selected by a pattern, moved into one directory, then fully restored by
// the single inverse command.
func TestStageMoveGlob_PlanAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	names := []string{"shot-a.png", "shot-b.png", "shot-c.png", "notes.txt"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	destDir := filepath.Join(dir, "screenshots")
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := stageMove(context.Background(), registry.Capability{}, map[string]any{
		"source_glob": filepath.Join(dir, "shot-*.png"),
		"destination": destDir,
	})
	if err != nil {
		t.Fatalf("stageMove (glob): %v", err)
	}

	// Forward must move exactly the three png matches (sorted) into destDir, with a
	// single "--" terminator and the directory as the trailing argument.
	wantFwd := Command{Binary: "mv", Args: []string{
		"--",
		filepath.Join(dir, "shot-a.png"),
		filepath.Join(dir, "shot-b.png"),
		filepath.Join(dir, "shot-c.png"),
		destDir,
	}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	wantInv := Command{Binary: "mv", Args: []string{
		"--",
		filepath.Join(destDir, "shot-a.png"),
		filepath.Join(destDir, "shot-b.png"),
		filepath.Join(destDir, "shot-c.png"),
		dir,
	}}
	if plan.Inverse == nil || !reflect.DeepEqual(*plan.Inverse, wantInv) {
		t.Errorf("Inverse = %+v, want %+v", plan.Inverse, wantInv)
	}

	// Round trip: forward moves the three pngs into destDir (notes.txt stays put),
	// inverse restores them.
	runPlanCommand(t, plan.Forward)
	for _, n := range []string{"shot-a.png", "shot-b.png", "shot-c.png"} {
		if pathExists(filepath.Join(dir, n)) || !pathExists(filepath.Join(destDir, n)) {
			t.Fatalf("after forward, %s not moved into destDir", n)
		}
	}
	if !pathExists(filepath.Join(dir, "notes.txt")) {
		t.Fatalf("non-matching notes.txt should not have moved")
	}
	runPlanCommand(t, *plan.Inverse)
	for _, n := range []string{"shot-a.png", "shot-b.png", "shot-c.png"} {
		if !pathExists(filepath.Join(dir, n)) || pathExists(filepath.Join(destDir, n)) {
			t.Fatalf("after undo, %s not restored to its parent", n)
		}
	}
}

// TestStageMoveGlob_Rejects covers the guardrails specific to a batch move.
func TestStageMoveGlob_Rejects(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "out")
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file occupying a destination name, to force a collision rejection.
	if err := os.WriteFile(filepath.Join(destDir, "a.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Two files in sibling subdirectories under a shared ancestor, so a wildcard
	// on the intermediate path segment matches across two different parents and
	// forces the shared-parent rejection. (filepath.Glob has no brace syntax, so
	// spanning parents requires an intermediate wildcard rather than two flat dirs.)
	span := t.TempDir()
	for _, sub := range []string{"d1", "d2"} {
		if err := os.Mkdir(filepath.Join(span, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(span, sub, "c.png"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := map[string]map[string]any{
		"both source and glob": {"source": filepath.Join(dir, "a.png"), "source_glob": filepath.Join(dir, "*.png"), "destination": destDir},
		"no matches":           {"source_glob": filepath.Join(dir, "nope-*.png"), "destination": destDir},
		"dash glob":            {"source_glob": "-rf*", "destination": destDir},
		"dest not existing":    {"source_glob": filepath.Join(dir, "*.png"), "destination": filepath.Join(dir, "ghost_dir")},
		"dest is a file":       {"source_glob": filepath.Join(dir, "*.png"), "destination": filepath.Join(dir, "a.png")},
		"dest equals parent":   {"source_glob": filepath.Join(dir, "*.png"), "destination": dir},
		"collision at dest":    {"source_glob": filepath.Join(dir, "a.png"), "destination": destDir},
		"spans directories":    {"source_glob": filepath.Join(span, "*", "*.png"), "destination": destDir},
	}
	for name, in := range cases {
		if _, err := stageMove(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestStageMove_WhitespaceHint confirms the diagnostic that fires when a single
// source path misses only because the typed name uses a different kind of space
// than the real file (the macOS-screenshot U+202F problem). The error must point
// at the real file and mention the narrow no-break space so the model knows to
// reach for source_glob.
func TestStageMove_WhitespaceHint(t *testing.T) {
	dir := t.TempDir()
	// Real file uses a narrow no-break space (U+202F) before "PM".
	actual := filepath.Join(dir, "Screenshot 2026-06-23 at 1.00.00 PM.png")
	if err := os.WriteFile(actual, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Caller supplies the same name but with an ordinary space.
	typed := filepath.Join(dir, "Screenshot 2026-06-23 at 1.00.00 PM.png")

	_, err := stageMove(context.Background(), registry.Capability{}, map[string]any{
		"source":      typed,
		"destination": filepath.Join(dir, "moved.png"),
	})
	if err == nil {
		t.Fatal("expected an error for the mistyped name")
	}
	msg := err.Error()
	if !strings.Contains(msg, "narrow no-break space") || !strings.Contains(msg, "source_glob") {
		t.Errorf("error message lacks the whitespace hint: %q", msg)
	}
}

// TestNormalizeWhitespace_CollapsesRuns guards the contract suggestWhitespaceMatch
// depends on: any run of whitespace — regardless of how many runes or which kinds
// — normalizes to exactly one ASCII space, so names differing only in their
// separators (a tab vs. two spaces, U+202F vs. an ordinary space) compare equal.
func TestNormalizeWhitespace_CollapsesRuns(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"single ordinary space":    {"a b", "a b"},
		"tab equals one space":     {"a\tb", "a b"},
		"run of spaces collapses":  {"a   b", "a b"},
		"mixed run collapses":      {"a \t b", "a b"},
		"narrow no-break space":    {"a\u202fb", "a b"},
		"leading and trailing run": {"  a\tb  ", " a b "},
		"no whitespace unchanged":  {"abc", "abc"},
	}
	for name, c := range cases {
		if got := normalizeWhitespace(c.in); got != c.want {
			t.Errorf("%s: normalizeWhitespace(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestStageCopy_PlanAndRoundTrip(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.txt")

	plan, err := stageCopy(context.Background(), registry.Capability{}, map[string]any{"source": src, "destination": dst})
	if err != nil {
		t.Fatalf("stageCopy: %v", err)
	}
	wantFwd := Command{Binary: "cp", Args: []string{"-R", "--", src, dst}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	// Inverse moves the freshly-made copy to the Trash (never an rm).
	if plan.Inverse == nil || plan.Inverse.Binary != "mv" || plan.Inverse.Args[len(plan.Inverse.Args)-2] != dst {
		t.Fatalf("Inverse = %+v, want mv of copy into Trash", plan.Inverse)
	}

	runPlanCommand(t, plan.Forward)
	if !pathExists(src) || !pathExists(dst) {
		t.Fatalf("after copy: original and copy should both exist (src=%v dst=%v)", pathExists(src), pathExists(dst))
	}
	runPlanCommand(t, *plan.Inverse)
	if !pathExists(src) {
		t.Errorf("undo must not touch the original")
	}
	if pathExists(dst) {
		t.Errorf("undo should have removed the copy from %q", dst)
	}
	trashDest := plan.Inverse.Args[len(plan.Inverse.Args)-1]
	if !pathExists(trashDest) {
		t.Errorf("copy should now be in the Trash at %q", trashDest)
	}
}

func TestStageRemove_PlanAndRoundTrip(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(target, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := stageRemove(context.Background(), registry.Capability{}, map[string]any{"path": target})
	if err != nil {
		t.Fatalf("stageRemove: %v", err)
	}
	// Forward recycles to the Trash (mv), never a hard rm.
	if plan.Forward.Binary != "mv" || plan.Forward.Args[len(plan.Forward.Args)-2] != target {
		t.Fatalf("Forward = %+v, want mv of target into Trash", plan.Forward)
	}
	trashDest := plan.Forward.Args[len(plan.Forward.Args)-1]

	runPlanCommand(t, plan.Forward)
	if pathExists(target) || !pathExists(trashDest) {
		t.Fatalf("after remove: target gone=%v in trash=%v", !pathExists(target), pathExists(trashDest))
	}
	runPlanCommand(t, *plan.Inverse)
	if !pathExists(target) || pathExists(trashDest) {
		t.Fatalf("after undo: target restored=%v trash empty=%v", pathExists(target), !pathExists(trashDest))
	}
}

func TestStageRemove_RejectsMissingAndDash(t *testing.T) {
	dir := t.TempDir()
	for name, in := range map[string]map[string]any{
		"missing path":     {},
		"nonexistent path": {"path": filepath.Join(dir, "ghost.txt")},
		"dash path":        {"path": "-rf"},
	} {
		if _, err := stageRemove(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestTrashPathFor_MissingTrashDir confirms staging fails with a clear error
// (rather than producing a doomed plan) when ~/.Trash is absent or is not a
// directory — the case Copilot flagged on PR #19. Both a missing directory and a
// regular file sitting where the Trash should be must be rejected.
func TestTrashPathFor_MissingTrashDir(t *testing.T) {
	// $HOME with no .Trash at all.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := trashPathFor("/somewhere/test.txt"); err == nil {
		t.Errorf("expected error when ~/.Trash is missing, got nil")
	}

	// $HOME where .Trash exists but is a regular file, not a directory.
	if err := os.WriteFile(filepath.Join(home, ".Trash"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := trashPathFor("/somewhere/test.txt"); err == nil {
		t.Errorf("expected error when ~/.Trash is not a directory, got nil")
	}
}

// TestStageRemove_FailsWhenTrashMissing confirms the validation surfaces through
// a real mutator: removing a file fails at STAGE time (not later) when there is
// nowhere to recycle to.
func TestStageRemove_FailsWhenTrashMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no .Trash created
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := stageRemove(context.Background(), registry.Capability{}, map[string]any{"path": target}); err == nil {
		t.Errorf("stageRemove should fail when ~/.Trash is unavailable")
	}
}

// TestTrashPathFor_CollisionSuffix confirms an existing same-named item in the
// Trash is not overwritten: the helper appends a numeric suffix instead.
func TestTrashPathFor_CollisionSuffix(t *testing.T) {
	home := redirectHomeWithTrash(t)
	occupied := filepath.Join(home, ".Trash", "test.txt")
	if err := os.WriteFile(occupied, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := trashPathFor("/somewhere/test.txt")
	if err != nil {
		t.Fatalf("trashPathFor: %v", err)
	}
	want := filepath.Join(home, ".Trash", "test 2.txt")
	if got != want {
		t.Errorf("trashPathFor with collision = %q, want %q", got, want)
	}
}

// --- write_file / append_to_file ---
//
// These two mutators carry model-controlled content on the forward command's
// stdin (Command.Stdin), so alongside the usual plan/rejection/round-trip
// coverage the tests here pin the injection contract: flag-like content lands
// in Stdin (pure data), never in argv, and the argv itself is exactly the
// fixed ["--", path] / ["-a", "--", path] shape.

// TestStageWriteFile_Plan confirms staging yields a tee forward carrying the
// content on stdin, a Trash-recycling inverse, and a preview that names the
// path without dumping the payload — all without touching the filesystem.
func TestStageWriteFile_Plan(t *testing.T) {
	redirectHomeWithTrash(t)
	target := filepath.Join(t.TempDir(), "notes.txt")
	content := "line one\nline two\n"

	plan, err := stageWriteFile(context.Background(), registry.Capability{}, map[string]any{"path": target, "content": content})
	if err != nil {
		t.Fatalf("stageWriteFile: %v", err)
	}
	wantArgs := []string{"--", target}
	if plan.Forward.Binary != "tee" || !reflect.DeepEqual(plan.Forward.Args, wantArgs) {
		t.Errorf("forward = %q %v, want tee %v", plan.Forward.Binary, plan.Forward.Args, wantArgs)
	}
	if string(plan.Forward.Stdin) != content {
		t.Errorf("forward stdin = %q, want the content verbatim", plan.Forward.Stdin)
	}
	if plan.Inverse == nil || plan.Inverse.Binary != "mv" || !strings.Contains(plan.Inverse.Args[2], ".Trash") {
		t.Errorf("inverse should mv the file into the Trash; got %+v", plan.Inverse)
	}
	if !strings.Contains(plan.Preview, target) {
		t.Errorf("preview should name the target: %q", plan.Preview)
	}
	if strings.Contains(plan.Preview, "line two") {
		t.Errorf("preview must not dump the full payload (only a first-line snippet): %q", plan.Preview)
	}
	if !plan.Forward.DiscardStdout {
		t.Error("forward must discard tee's stdout echo of the written content")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("staging must not create the file; stat err = %v", err)
	}
}

// TestStageWriteFile_FlagLikeContentIsStdinData is the option-injection
// regression for the stdin seam: content that looks like osascript/tee flags
// must land byte-for-byte in Stdin while argv stays the fixed two-token shape.
func TestStageWriteFile_FlagLikeContentIsStdinData(t *testing.T) {
	redirectHomeWithTrash(t)
	target := filepath.Join(t.TempDir(), "f.txt")
	hostile := "-e do shell script\n--append\n$(rm -rf ~)\n"

	plan, err := stageWriteFile(context.Background(), registry.Capability{}, map[string]any{"path": target, "content": hostile})
	if err != nil {
		t.Fatalf("stageWriteFile: %v", err)
	}
	if string(plan.Forward.Stdin) != hostile {
		t.Errorf("hostile content must be carried verbatim as stdin data; got %q", plan.Forward.Stdin)
	}
	if !reflect.DeepEqual(plan.Forward.Args, []string{"--", target}) {
		t.Errorf("argv must stay fixed regardless of content; got %v", plan.Forward.Args)
	}
}

// TestStageWriteFile_Rejects covers the guardrail table: bad paths, an occupied
// target (including a dangling symlink, which Lstat must count as occupied), a
// missing parent, and oversize content.
func TestStageWriteFile_Rejects(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	existing := filepath.Join(dir, "have.txt")
	if err := os.WriteFile(existing, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "no-such-target"), dangling); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   map[string]any
	}{
		{"missing_path", map[string]any{"content": "x"}},
		{"dash_leading_path", map[string]any{"path": "-rf", "content": "x"}},
		{"existing_target", map[string]any{"path": existing, "content": "x"}},
		{"dangling_symlink_target", map[string]any{"path": dangling, "content": "x"}},
		{"missing_parent", map[string]any{"path": filepath.Join(dir, "no", "such", "file.txt"), "content": "x"}},
		{"oversize_content", map[string]any{"path": filepath.Join(dir, "big.txt"), "content": strings.Repeat("x", maxWriteContentBytes+1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := stageWriteFile(context.Background(), registry.Capability{}, tc.in); err == nil {
				t.Fatalf("expected stageWriteFile to reject %s", tc.name)
			}
		})
	}
}

// TestWriteFile_RoundTrip executes the real flow: forward creates the file with
// the exact bytes (via RunCommand's stdin path), inverse recycles it into the
// sandbox Trash.
func TestWriteFile_RoundTrip(t *testing.T) {
	home := redirectHomeWithTrash(t)
	target := filepath.Join(t.TempDir(), "made.txt")
	content := "alpha\nbeta\n"
	eng := New()
	ctx := context.Background()

	plan, err := eng.Stage(ctx, registry.Capability{Name: "write_file", Builder: "write_file", Params: []registry.ParamSpec{
		{Name: "path", Type: registry.TypePath, Required: true, Arg: registry.ArgRule{Kind: registry.ArgNone}},
		{Name: "content", Type: registry.TypeString, Required: true, Arg: registry.ArgRule{Kind: registry.ArgNone}},
	}}, map[string]any{"path": target, "content": content})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := eng.RunCommand(ctx, plan.Forward); err != nil {
		t.Fatalf("RunCommand(forward): %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != content {
		t.Fatalf("file content = %q (err %v), want %q", got, err, content)
	}
	if _, err := eng.RunCommand(ctx, *plan.Inverse); err != nil {
		t.Fatalf("RunCommand(inverse): %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("undo should have removed the created file; stat err = %v", err)
	}
	trashed, err := os.ReadFile(filepath.Join(home, ".Trash", "made.txt"))
	if err != nil || string(trashed) != content {
		t.Errorf("undo should have recycled the file into the sandbox Trash; read err = %v", err)
	}
}

// TestStageAppendToFile_Plan confirms staging bakes the target's CURRENT bytes
// into a truncating tee inverse, so undo is a byte-exact restore.
func TestStageAppendToFile_Plan(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "log.txt")
	prior := "existing line\n"
	if err := os.WriteFile(target, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := stageAppendToFile(context.Background(), registry.Capability{}, map[string]any{"path": target, "content": "new line\n"})
	if err != nil {
		t.Fatalf("stageAppendToFile: %v", err)
	}
	if plan.Forward.Binary != "tee" || !reflect.DeepEqual(plan.Forward.Args, []string{"-a", "--", target}) {
		t.Errorf("forward = %q %v, want tee [-a -- %s]", plan.Forward.Binary, plan.Forward.Args, target)
	}
	if string(plan.Forward.Stdin) != "new line\n" {
		t.Errorf("forward stdin = %q", plan.Forward.Stdin)
	}
	if plan.Inverse == nil || !reflect.DeepEqual(plan.Inverse.Args, []string{"--", target}) || string(plan.Inverse.Stdin) != prior {
		t.Errorf("inverse must rewrite the prior bytes exactly; got %+v", plan.Inverse)
	}
	// Both directions must suppress tee's echo: the forward echo is redundant,
	// and the inverse echo would LEAK the file's prior contents to the client.
	if !plan.Forward.DiscardStdout || !plan.Inverse.DiscardStdout {
		t.Error("append forward and inverse must both discard tee's stdout echo")
	}
	if !strings.Contains(plan.Preview, "prior contents") {
		t.Errorf("preview should state undo restores prior contents: %q", plan.Preview)
	}
}

// TestStageAppendToFile_Rejects covers the guardrail table for append: the
// target must be an existing regular file within the size cap, and the content
// must be present, non-empty, and within its cap.
func TestStageAppendToFile_Rejects(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, maxAppendTargetBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   map[string]any
	}{
		{"missing_path", map[string]any{"content": "x"}},
		{"nonexistent_target", map[string]any{"path": filepath.Join(dir, "nope.txt"), "content": "x"}},
		{"dash_leading_path", map[string]any{"path": "-x", "content": "x"}},
		{"directory_target", map[string]any{"path": dir, "content": "x"}},
		{"empty_content", map[string]any{"path": target, "content": ""}},
		{"oversize_content", map[string]any{"path": target, "content": strings.Repeat("x", maxWriteContentBytes+1)}},
		{"oversize_target", map[string]any{"path": big, "content": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := stageAppendToFile(context.Background(), registry.Capability{}, tc.in); err == nil {
				t.Fatalf("expected stageAppendToFile to reject %s", tc.name)
			}
		})
	}
}

// TestRunCommand_DiscardStdoutSuppressesEcho proves the leak fix end to end: a
// tee command that would echo its (potentially sensitive) stdin returns no
// content when DiscardStdout is set — the file is still written, but nothing of
// it comes back through the tool result.
func TestRunCommand_DiscardStdoutSuppressesEcho(t *testing.T) {
	target := filepath.Join(t.TempDir(), "quiet.txt")
	secret := "s3cret prior contents\n"
	out, err := New().RunCommand(context.Background(), Command{
		Binary: "tee", Args: []string{"--", target}, Stdin: []byte(secret), DiscardStdout: true,
	})
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if strings.Contains(out, "s3cret") {
		t.Errorf("stdout echo must be discarded; got %q", out)
	}
	if got, _ := os.ReadFile(target); string(got) != secret {
		t.Errorf("file must still be written; got %q", got)
	}
}

// --- compress / extract (bsdtar) ---
//
// These two mutators shell out to /usr/bin/tar. The tests pin the exact argv
// shape (so a future edit can't drop the "--" terminator or the secure default),
// prove a real forward→inverse round trip on disk, exercise the guardrail table,
// and — most importantly — prove bsdtar's zip-slip refusal with a crafted hostile
// archive so extract can never write outside its destination.

// writeTarGz writes a gzip-compressed tar at path containing the given members
// (name → contents). It is the test's archive fixture builder: it can create
// perfectly ordinary archives for the round-trip tests AND deliberately hostile
// ones (member names with ".." or a leading "/") for the zip-slip test, since
// archive/tar writes whatever member name it is handed.
func writeTarGz(t *testing.T, path string, members map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating archive %q: %v", path, err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range members {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}
}

// writePlainTar writes an UNCOMPRESSED tar at path containing the given members
// (name -> contents). It is the fixture for the plain-".tar" extraction test:
// byte-for-byte the same container writeTarGz produces, just without the gzip
// wrapper, which is exactly the difference extract has to tolerate.
func writePlainTar(t *testing.T, path string, members map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating archive %q: %v", path, err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	for name, body := range members {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar: %v", err)
	}
}

// TestStageCompress_PlanAndRoundTrip pins compress's argv shape and proves the
// archive is created by the forward command and recycled to the Trash by undo.
func TestStageCompress_PlanAndRoundTrip(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "project")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(srcDir, n), []byte(n+" contents\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(dir, "project.zip")

	plan, err := stageCompress(context.Background(), registry.Capability{}, map[string]any{
		"archive": archive,
		"sources": []string{srcDir},
	})
	if err != nil {
		t.Fatalf("stageCompress: %v", err)
	}
	wantFwd := Command{Binary: "tar", Args: []string{"-a", "-c", "-f", archive, "-C", dir, "--", "project"}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	if plan.Inverse == nil || plan.Inverse.Binary != "mv" || plan.Inverse.Args[len(plan.Inverse.Args)-2] != archive {
		t.Fatalf("Inverse = %+v, want mv of the archive into the Trash", plan.Inverse)
	}
	if strings.Contains(plan.Preview, "contents") {
		t.Errorf("preview must not dump file contents: %q", plan.Preview)
	}
	if pathExists(archive) {
		t.Fatal("staging must not create the archive")
	}

	// Forward creates the archive; inverse recycles it into the sandbox Trash.
	runPlanCommand(t, plan.Forward)
	if !pathExists(archive) {
		t.Fatalf("after forward, archive %q should exist", archive)
	}
	runPlanCommand(t, *plan.Inverse)
	if pathExists(archive) {
		t.Errorf("undo should have removed the archive from %q", archive)
	}
	if trashDest := plan.Inverse.Args[len(plan.Inverse.Args)-1]; !pathExists(trashDest) {
		t.Errorf("archive should now be in the Trash at %q", trashDest)
	}
}

// TestStageCompress_MultipleSourcesSharedParent proves several sources sharing
// one parent are archived by basename after a single "--", in sorted order.
func TestStageCompress_MultipleSourcesSharedParent(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	for _, n := range []string{"b.txt", "a.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := filepath.Join(dir, "bundle.tar.gz")

	plan, err := stageCompress(context.Background(), registry.Capability{}, map[string]any{
		"archive": archive,
		"sources": []string{filepath.Join(dir, "b.txt"), filepath.Join(dir, "a.txt"), filepath.Join(dir, "c.txt")},
	})
	if err != nil {
		t.Fatalf("stageCompress: %v", err)
	}
	wantFwd := Command{Binary: "tar", Args: []string{"-a", "-c", "-f", archive, "-C", dir, "--", "a.txt", "b.txt", "c.txt"}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v (basenames must be sorted, after a single --)", plan.Forward, wantFwd)
	}
}

// TestStageCompress_Rejects covers the guardrail table for archive creation.
func TestStageCompress_Rejects(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(dir, "have.zip")
	if err := os.WriteFile(occupied, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Two sources under different parents, to force the shared-parent rejection.
	other := t.TempDir()
	otherSrc := filepath.Join(other, "far.txt")
	if err := os.WriteFile(otherSrc, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]any{
		"missing archive": {"sources": []string{src}},
		"dash archive":    {"archive": "-out.zip", "sources": []string{src}},
		"bad extension":   {"archive": filepath.Join(dir, "out.rar"), "sources": []string{src}},
		// A plain .tar is extractable but NOT creatable: compress always compresses.
		"uncompressed tar":       {"archive": filepath.Join(dir, "out.tar"), "sources": []string{src}},
		"archive exists":         {"archive": occupied, "sources": []string{src}},
		"archive parent missing": {"archive": filepath.Join(dir, "no", "out.zip"), "sources": []string{src}},
		"missing sources":        {"archive": filepath.Join(dir, "out.zip")},
		"empty sources":          {"archive": filepath.Join(dir, "out.zip"), "sources": []string{}},
		"dash source":            {"archive": filepath.Join(dir, "out.zip"), "sources": []string{"-rf"}},
		"nonexistent source":     {"archive": filepath.Join(dir, "out.zip"), "sources": []string{filepath.Join(dir, "ghost.txt")}},
		"sources span parents":   {"archive": filepath.Join(dir, "out.zip"), "sources": []string{src, otherSrc}},
		"duplicate source":       {"archive": filepath.Join(dir, "out.zip"), "sources": []string{src, src}},
		"archive inside source":  {"archive": filepath.Join(dir, "out.zip"), "sources": []string{dir}},
	}
	for name, in := range cases {
		if _, err := stageCompress(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestStageExtract_PlanAndRoundTrip pins extract's argv shape and proves the
// archive is unpacked into the empty destination and undone by recycling the
// whole destination to the Trash.
func TestStageExtract_PlanAndRoundTrip(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	archive := filepath.Join(dir, "payload.tar.gz")
	writeTarGz(t, archive, map[string]string{"data/a.txt": "alpha\n", "data/b.txt": "beta\n"})
	dest := filepath.Join(dir, "out")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := stageExtract(context.Background(), registry.Capability{}, map[string]any{
		"archive":     archive,
		"destination": dest,
	})
	if err != nil {
		t.Fatalf("stageExtract: %v", err)
	}
	wantFwd := Command{Binary: "tar", Args: []string{"-x", "-f", archive, "-C", dest}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v", plan.Forward, wantFwd)
	}
	wantInv := Command{Binary: "mv", Args: []string{"--", dest, plan.Inverse.Args[len(plan.Inverse.Args)-1]}}
	if plan.Inverse == nil || !reflect.DeepEqual(*plan.Inverse, wantInv) {
		t.Fatalf("Inverse = %+v, want mv of the destination into the Trash", plan.Inverse)
	}

	// Forward unpacks the members; inverse recycles the whole destination.
	runPlanCommand(t, plan.Forward)
	got, err := os.ReadFile(filepath.Join(dest, "data", "a.txt"))
	if err != nil || string(got) != "alpha\n" {
		t.Fatalf("extracted content = %q (err %v), want %q", got, err, "alpha\n")
	}
	runPlanCommand(t, *plan.Inverse)
	if pathExists(dest) {
		t.Errorf("undo should have moved the destination away from %q", dest)
	}
	trashDest := plan.Inverse.Args[len(plan.Inverse.Args)-1]
	if !pathExists(filepath.Join(trashDest, "data", "a.txt")) {
		t.Errorf("undo should have recycled the unpacked tree into the Trash at %q", trashDest)
	}
}

// TestStageExtract_PlainTarRoundTrip is the regression for the reported bug: an
// uncompressed ".tar" (e.g. a downloaded "condvar.tar") was refused outright,
// forcing the user out to a raw shell. It must now stage like any other archive
// and really unpack, with the SAME argv as the gzip case — bsdtar autodetects
// the container, so no format flag is added or needed.
func TestStageExtract_PlainTarRoundTrip(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	archive := filepath.Join(dir, "condvar.tar")
	writePlainTar(t, archive, map[string]string{"src/main.c": "int main(void){}\n"})
	dest := filepath.Join(dir, "out")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := stageExtract(context.Background(), registry.Capability{}, map[string]any{
		"archive":     archive,
		"destination": dest,
	})
	if err != nil {
		t.Fatalf("stageExtract on a plain .tar: %v", err)
	}
	wantFwd := Command{Binary: "tar", Args: []string{"-x", "-f", archive, "-C", dest}}
	if !reflect.DeepEqual(plan.Forward, wantFwd) {
		t.Errorf("Forward = %+v, want %+v (identical to the .tar.gz case)", plan.Forward, wantFwd)
	}
	runPlanCommand(t, plan.Forward)
	got, err := os.ReadFile(filepath.Join(dest, "src", "main.c"))
	if err != nil || string(got) != "int main(void){}\n" {
		t.Fatalf("extracted content = %q (err %v), want the member body", got, err)
	}
}

// TestStageExtract_UppercaseTar proves the plain-tar suffix is matched
// case-insensitively, like every other supported extension.
func TestStageExtract_UppercaseTar(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	archive := filepath.Join(dir, "ARCHIVE.TAR")
	writePlainTar(t, archive, map[string]string{"f.txt": "x"})
	dest := filepath.Join(dir, "out")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := stageExtract(context.Background(), registry.Capability{}, map[string]any{
		"archive": archive, "destination": dest,
	}); err != nil {
		t.Fatalf("stageExtract on %q: %v", archive, err)
	}
}

// TestStageExtract_Rejects covers the guardrail table for extraction.
func TestStageExtract_Rejects(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	archive := filepath.Join(dir, "ok.tar.gz")
	writeTarGz(t, archive, map[string]string{"f.txt": "x"})
	emptyDest := filepath.Join(dir, "empty")
	if err := os.Mkdir(emptyDest, 0o755); err != nil {
		t.Fatal(err)
	}
	fullDest := filepath.Join(dir, "full")
	if err := os.Mkdir(fullDest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fullDest, "prior.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	notArchive := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notArchive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileAsDest := filepath.Join(dir, "afile.txt")
	if err := os.WriteFile(fileAsDest, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real file whose suffix sits just outside the allowlist: it exists and is a
	// regular file, so only the extension check can be what rejects it.
	bz2Archive := filepath.Join(dir, "ok.tar.bz2")
	if err := os.WriteFile(bz2Archive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]map[string]any{
		"missing archive":       {"destination": emptyDest},
		"dash archive":          {"archive": "-a.zip", "destination": emptyDest},
		"nonexistent archive":   {"archive": filepath.Join(dir, "ghost.zip"), "destination": emptyDest},
		"bad extension archive": {"archive": notArchive, "destination": emptyDest},
		// The widening is exactly one format: .tar.bz2 and friends stay refused.
		"unsupported bz2":     {"archive": bz2Archive, "destination": emptyDest},
		"archive is a dir":    {"archive": emptyDest, "destination": emptyDest},
		"missing destination": {"archive": archive},
		"dash destination":    {"archive": archive, "destination": "-d"},
		"nonexistent dest":    {"archive": archive, "destination": filepath.Join(dir, "ghost_dir")},
		"dest is a file":      {"archive": archive, "destination": fileAsDest},
		"dest not empty":      {"archive": archive, "destination": fullDest},
	}
	for name, in := range cases {
		if _, err := stageExtract(context.Background(), registry.Capability{}, in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestStageExtract_RefusesZipSlip is the security regression: a crafted archive
// whose members try to escape the destination via a ".." component or an absolute
// path must NOT write outside the destination. bsdtar's secure default (no -P)
// refuses the ".." member outright and strips the leading "/" from the absolute
// member so it lands harmlessly INSIDE the destination. The forward command may
// exit non-zero because of the refused member, so we run it directly and assert
// on the resulting filesystem state rather than on the exit code.
func TestStageExtract_RefusesZipSlip(t *testing.T) {
	redirectHomeWithTrash(t)
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.tar.gz")
	writeTarGz(t, archive, map[string]string{
		"safe.txt":          "ok\n",
		"../escape.txt":     "pwned\n", // must be refused
		"/tmp/abs_evil.txt": "pwned\n", // leading slash stripped → contained
	})
	dest := filepath.Join(dir, "unpack")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := stageExtract(context.Background(), registry.Capability{}, map[string]any{
		"archive": archive, "destination": dest,
	})
	if err != nil {
		t.Fatalf("stageExtract: %v", err)
	}
	// Run the forward command directly; bsdtar returns a delayed non-zero exit for
	// the refused member, which is expected — the assertion is about the disk.
	bin, err := policy.ResolveBinary(plan.Forward.Binary)
	if err != nil {
		t.Fatalf("resolving tar: %v", err)
	}
	_ = exec.Command(bin, plan.Forward.Args...).Run()

	// The benign member landed inside the destination.
	if !pathExists(filepath.Join(dest, "safe.txt")) {
		t.Errorf("the in-bounds member should have been extracted into the destination")
	}
	// The ".." member must NOT have escaped to the destination's parent.
	if pathExists(filepath.Join(dir, "escape.txt")) {
		t.Errorf("zip-slip: a '..' member escaped the destination to %q", filepath.Join(dir, "escape.txt"))
	}
	// The absolute member must NOT have been written to the real /tmp: bsdtar
	// strips the leading "/" so it lands inside the destination instead. We only
	// ASSERT here — never delete the path — so a failing run can't remove an
	// unrelated file that happens to sit outside this test's temp directory.
	if pathExists("/tmp/abs_evil.txt") {
		t.Errorf("zip-slip: an absolute-path member escaped to /tmp/abs_evil.txt (leading '/' should have been stripped, landing it inside the destination)")
	}
}

// TestArchiveExtension pins BOTH closed format allowlists and the asymmetry
// between them: extract reads an uncompressed ".tar" that compress will not
// create, and neither direction accepts anything outside its own list.
//
// It also pins the reported suffix, not just the yes/no: "backup.tar.gz" must
// report ".tar.gz" and never be misread as a plain ".tar" — the ordering
// invariant that keeps a gzip tarball out of the plain-tar branch.
func TestArchiveExtension(t *testing.T) {
	cases := []struct {
		path    string
		allowed []string
		want    string // "" means the path must be rejected
	}{
		// Creating: the three compressed formats, case-insensitively.
		{"a.zip", creatableArchiveExtensions, ".zip"},
		{"A.ZIP", creatableArchiveExtensions, ".zip"},
		{"b.tar.gz", creatableArchiveExtensions, ".tar.gz"},
		{"b.TAR.GZ", creatableArchiveExtensions, ".tar.gz"},
		{"c.tgz", creatableArchiveExtensions, ".tgz"},
		{"/x/y/report.zip", creatableArchiveExtensions, ".zip"},
		// Creating: a plain tar is deliberately NOT creatable.
		{"a.tar", creatableArchiveExtensions, ""},
		{"a.rar", creatableArchiveExtensions, ""},
		{"a.7z", creatableArchiveExtensions, ""},
		{"a.gz", creatableArchiveExtensions, ""},
		{"a.zipx", creatableArchiveExtensions, ""},
		{"noext", creatableArchiveExtensions, ""},
		{"a.tar.bz2", creatableArchiveExtensions, ""},
		// Extracting: everything creatable, plus the plain tar.
		{"a.zip", extractableArchiveExtensions, ".zip"},
		{"b.tar.gz", extractableArchiveExtensions, ".tar.gz"},
		{"b.TAR.GZ", extractableArchiveExtensions, ".tar.gz"},
		{"c.tgz", extractableArchiveExtensions, ".tgz"},
		{"a.tar", extractableArchiveExtensions, ".tar"},
		{"A.TAR", extractableArchiveExtensions, ".tar"},
		{"/x/y/condvar.tar", extractableArchiveExtensions, ".tar"},
		// Extracting: still a closed set — the widening is exactly one format.
		{"a.rar", extractableArchiveExtensions, ""},
		{"a.7z", extractableArchiveExtensions, ""},
		{"a.gz", extractableArchiveExtensions, ""},
		{"a.zipx", extractableArchiveExtensions, ""},
		{"noext", extractableArchiveExtensions, ""},
		{"a.tar.bz2", extractableArchiveExtensions, ""},
		{"a.tarball", extractableArchiveExtensions, ""},
	}
	for _, c := range cases {
		got, matched := archiveExtension(c.path, c.allowed)
		if c.want == "" {
			if matched {
				t.Errorf("archiveExtension(%q, %v) = %q, true; want no match", c.path, c.allowed, got)
			}
			continue
		}
		if !matched || got != c.want {
			t.Errorf("archiveExtension(%q, %v) = %q, %v; want %q, true", c.path, c.allowed, got, matched, c.want)
		}
	}
}

// TestAppendToFile_RoundTrip executes the real flow: forward appends, inverse
// restores the prior bytes exactly — a snapshot restore, as the preview states.
func TestAppendToFile_RoundTrip(t *testing.T) {
	target := filepath.Join(t.TempDir(), "notes.txt")
	prior := "hello\n"
	if err := os.WriteFile(target, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := New()
	ctx := context.Background()

	plan, err := stageAppendToFile(ctx, registry.Capability{}, map[string]any{"path": target, "content": "world\n"})
	if err != nil {
		t.Fatalf("stageAppendToFile: %v", err)
	}
	if _, err := eng.RunCommand(ctx, plan.Forward); err != nil {
		t.Fatalf("RunCommand(forward): %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "hello\nworld\n" {
		t.Fatalf("after append content = %q, want %q", got, "hello\nworld\n")
	}
	if _, err := eng.RunCommand(ctx, *plan.Inverse); err != nil {
		t.Fatalf("RunCommand(inverse): %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != prior {
		t.Errorf("undo should restore prior bytes exactly; got %q, want %q", got, prior)
	}
}
