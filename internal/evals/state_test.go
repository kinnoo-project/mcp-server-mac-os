// state_test.go exercises CheckState against real temp directories. Unlike
// CheckExpectation (pure, no I/O), CheckState touches the filesystem, so it is
// tested with actual files/dirs rather than a hand-built value.
package evals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// identitySubst is a no-op substituter for tests that use already-resolved paths.
func identitySubst(s string) string { return s }

func TestCheckState_NilChecksNothing(t *testing.T) {
	if err := CheckState(nil, identitySubst); err != nil {
		t.Errorf("nil State should check nothing, got %v", err)
	}
}

func TestCheckState_ExistsAndAbsentAndIsDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "present.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	st := &State{
		Exists: []string{file, sub},
		Absent: []string{filepath.Join(dir, "nope.txt")},
		IsDir:  []string{sub},
	}
	if err := CheckState(st, identitySubst); err != nil {
		t.Errorf("expected all conditions to hold, got %v", err)
	}
}

func TestCheckState_ExistsFailsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	st := &State{Exists: []string{filepath.Join(dir, "ghost")}}
	if err := CheckState(st, identitySubst); err == nil {
		t.Fatal("expected an error when an Exists path is missing")
	}
}

func TestCheckState_AbsentFailsWhenPresent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "there.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &State{Absent: []string{file}}
	if err := CheckState(st, identitySubst); err == nil {
		t.Fatal("expected an error when an Absent path exists")
	}
}

func TestCheckState_IsDirFailsForFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &State{IsDir: []string{file}}
	if err := CheckState(st, identitySubst); err == nil {
		t.Fatal("expected an error when an is_dir path is a regular file")
	}
}

// TestCheckState_SubstitutionApplied confirms placeholders in State paths are
// resolved by the passed substituter before stat — the same {{scratch}} that a
// case's setup produces.
func TestCheckState_SubstitutionApplied(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "archive", "a.png")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	subst := func(s string) string { return strings.ReplaceAll(s, "{{scratch}}", dir) }

	st := &State{Exists: []string{"{{scratch}}/archive/a.png"}}
	if err := CheckState(st, subst); err != nil {
		t.Errorf("expected substituted path to resolve and exist, got %v", err)
	}
}
