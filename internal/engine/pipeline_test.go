// pipeline_test.go exercises RunPipeline end to end against the real, shipped
// registry capabilities (find, wc, grep, sort, head, mkdir, pwd, ...) and a
// hermetic temp tree — no mocks, the same philosophy as engine_test.go and
// capabilities_test.go.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// lookupCapability loads the embedded registry and returns the named
// capability, failing the test if it isn't found.
func lookupCapability(t *testing.T, name string) registry.Capability {
	t.Helper()
	r, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}
	c, ok := r.Lookup(name)
	if !ok {
		t.Fatalf("capability %q not found in registry", name)
	}
	return c
}

// TestSupportsPipeline_EligibilityMatchesDesign confirms the read-only,
// argv-builder-only eligibility rule against the real manifest: ordinary
// read-only capabilities are eligible, builtins (no subprocess to pipe) and
// mutators (don't run in one step) are not.
func TestSupportsPipeline_EligibilityMatchesDesign(t *testing.T) {
	eng := New()
	cases := []struct {
		name string
		want bool
	}{
		{"find", true},
		{"wc", true},
		{"grep", true},
		{"sort", true},
		{"head", true},
		{"ls", true},
		{"pwd", false},           // builtin: no subprocess to pipe
		{"largest_files", false}, // builtin
		{"mkdir", false},         // mutator: doesn't run in one step
		{"write_setting", false}, // mutator
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := lookupCapability(t, tc.name)
			if got := eng.SupportsPipeline(c); got != tc.want {
				t.Errorf("SupportsPipeline(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// makeFiles creates n empty files named prefix0..prefixN-1 under dir.
func makeFiles(t *testing.T, dir, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		path := filepath.Join(dir, prefix+string(rune('a'+i))+".txt")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", path, err)
		}
	}
}

// TestRunPipeline_FindThenWc drives a real two-stage pipeline: find lists
// matching files (one path per line), wc -l counts them. This is the actual
// motivating idiom (a find/filter stage feeding a downstream filter) the
// pipeline tool exists for.
func TestRunPipeline_FindThenWc(t *testing.T) {
	dir := t.TempDir()
	makeFiles(t, dir, "f", 3)

	find := lookupCapability(t, "find")
	wc := lookupCapability(t, "wc")

	out, err := New().RunPipeline(context.Background(), []PipelineStage{
		{Capability: find, Params: map[string]any{"path": dir, "extensions": []any{"txt"}}},
		{Capability: wc, Params: map[string]any{"lines": true}}, // no paths: reads find's piped output
	})
	if err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}
	if !strings.Contains(out, "3") {
		t.Errorf("expected wc -l to report 3 files, got %q", out)
	}
}

// TestRunPipeline_MaxStagesExceeded confirms a pipeline longer than
// MaxPipelineStages is rejected before anything runs.
func TestRunPipeline_MaxStagesExceeded(t *testing.T) {
	head := lookupCapability(t, "head")
	dir := t.TempDir()
	makeFiles(t, dir, "f", 1)
	stages := make([]PipelineStage, MaxPipelineStages+1)
	for i := range stages {
		stages[i] = PipelineStage{Capability: head, Params: map[string]any{"paths": []any{filepath.Join(dir, "fa.txt")}}}
	}
	if _, err := New().RunPipeline(context.Background(), stages); err == nil {
		t.Fatal("expected an error for exceeding MaxPipelineStages")
	}
}

// TestRunPipeline_FirstStageMissingInputErrors confirms an AcceptsStdin
// capability used as stage 0 with no positional input fails fast with a clear
// error, rather than hanging on a stdin read that will never resolve (there
// is no prior stage to supply it).
func TestRunPipeline_FirstStageMissingInputErrors(t *testing.T) {
	sortCap := lookupCapability(t, "sort")
	_, err := New().RunPipeline(context.Background(), []PipelineStage{
		{Capability: sortCap, Params: map[string]any{"numeric": true}}, // no paths, and it's stage 0
	})
	if err == nil {
		t.Fatal("expected an error: first stage has no input and no piped data is available")
	}
	if !strings.Contains(err.Error(), "first stage") {
		t.Errorf("error should explain this is the first-stage case, got: %v", err)
	}
}

// TestRunPipeline_RejectsBuiltinStage confirms a builtin (no subprocess, so
// nothing to pipe) cannot be used as a pipeline stage.
func TestRunPipeline_RejectsBuiltinStage(t *testing.T) {
	pwd := lookupCapability(t, "pwd")
	if _, err := New().RunPipeline(context.Background(), []PipelineStage{{Capability: pwd}}); err == nil {
		t.Fatal("expected an error: pwd is a builtin and cannot be a pipeline stage")
	}
}

// TestRunPipeline_RejectsMutatorStage confirms a mutator (stage/commit, not a
// single-step run) cannot be used as a pipeline stage.
func TestRunPipeline_RejectsMutatorStage(t *testing.T) {
	mkdir := lookupCapability(t, "mkdir")
	if _, err := New().RunPipeline(context.Background(), []PipelineStage{
		{Capability: mkdir, Params: map[string]any{"path": filepath.Join(t.TempDir(), "x")}},
	}); err == nil {
		t.Fatal("expected an error: mkdir is a mutator and cannot be a pipeline stage")
	}
}

// TestRunPipeline_StageFailureAbortsWithExitCode confirms a stage that exits
// non-zero aborts the whole pipeline with an error naming the stage and exit
// code, rather than feeding garbage forward.
func TestRunPipeline_StageFailureAbortsWithExitCode(t *testing.T) {
	grep := lookupCapability(t, "grep")
	_, err := New().RunPipeline(context.Background(), []PipelineStage{
		{Capability: grep, Params: map[string]any{
			"pattern": "x",
			"paths":   []any{"/no/such/path/should/exist/anywhere"},
		}},
	})
	if err == nil {
		t.Fatal("expected an error: grep on a nonexistent path exits non-zero")
	}
	if !strings.Contains(err.Error(), "stage 1") {
		t.Errorf("error should name the failing stage, got: %v", err)
	}
}

// TestRunPipeline_IntermediateSizeCapEnforced confirms a NON-FINAL stage whose
// raw output exceeds the intermediate cap aborts the pipeline rather than
// silently feeding an unbounded amount of data into memory or the next stage.
// The cap is intermediate-only — see TestRunPipeline_FinalStageNotSizeCapped
// for the final stage's contrasting, uncapped behavior.
func TestRunPipeline_IntermediateSizeCapEnforced(t *testing.T) {
	orig := maxPipelineStageBytes
	maxPipelineStageBytes = 16 // tiny, so a handful of files' worth of paths exceeds it
	t.Cleanup(func() { maxPipelineStageBytes = orig })

	dir := t.TempDir()
	makeFiles(t, dir, "f", 5)
	find := lookupCapability(t, "find")
	wc := lookupCapability(t, "wc")

	_, err := New().RunPipeline(context.Background(), []PipelineStage{
		{Capability: find, Params: map[string]any{"path": dir}}, // non-final: subject to the cap
		{Capability: wc, Params: map[string]any{"lines": true}},
	})
	if err == nil {
		t.Fatal("expected an error: the first (non-final) stage's output should exceed the lowered intermediate cap")
	}
	if !strings.Contains(err.Error(), "exceeding") {
		t.Errorf("error should explain the size cap was exceeded, got: %v", err)
	}
}

// TestRunPipeline_FinalStageNotSizeCapped confirms the intermediate cap does
// NOT apply to a pipeline's last stage (nor to a single-stage pipeline,
// stage 0 being both first and last) — its raw output goes straight to the
// same compaction a standalone Run call uses, which has no pre-compaction
// cap of its own, so a pipeline's final stage shouldn't behave differently
// from Run just because it arrived via RunPipeline.
func TestRunPipeline_FinalStageNotSizeCapped(t *testing.T) {
	orig := maxPipelineStageBytes
	maxPipelineStageBytes = 16 // tiny — would fail instantly if mistakenly applied to the final stage
	t.Cleanup(func() { maxPipelineStageBytes = orig })

	dir := t.TempDir()
	makeFiles(t, dir, "f", 5) // find's output here is well over 16 bytes
	find := lookupCapability(t, "find")

	out, err := New().RunPipeline(context.Background(), []PipelineStage{
		{Capability: find, Params: map[string]any{"path": dir}}, // sole stage: also the final stage
	})
	if err != nil {
		t.Fatalf("a single-stage pipeline's output must not be subject to the intermediate cap: %v", err)
	}
	if out == "" {
		t.Error("expected find's real output, got empty string")
	}
}
