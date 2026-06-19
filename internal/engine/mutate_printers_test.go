// mutate_printers_test.go tests the print mutators' validation and `lp` command
// construction without ever executing a plan (so nothing is ever printed). It
// covers the path/printer/copies guardrails, the "--"-terminated argv ordering,
// and the test page's scratch-file staging.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLpArgs_Ordering(t *testing.T) {
	// Default printer, single copy: just the terminator and file.
	if got := lpArgs("", 1, "/tmp/a.pdf"); !reflect.DeepEqual(got, []string{"--", "/tmp/a.pdf"}) {
		t.Errorf("lpArgs default/1 = %v", got)
	}
	// Named printer, multiple copies: -d, -n, then -- file.
	got := lpArgs("Office", 3, "/tmp/a.pdf")
	want := []string{"-d", "Office", "-n", "3", "--", "/tmp/a.pdf"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lpArgs = %v, want %v", got, want)
	}
}

func TestValidateCopies(t *testing.T) {
	if n, err := validateCopies(map[string]any{}); err != nil || n != 1 {
		t.Errorf("absent copies should default to 1, got %d, %v", n, err)
	}
	if n, err := validateCopies(map[string]any{"copies": 5}); err != nil || n != 5 {
		t.Errorf("copies=5 = %d, %v", n, err)
	}
	for _, bad := range []int{0, -1, maxPrintCopies + 1} {
		if _, err := validateCopies(map[string]any{"copies": bad}); err == nil {
			t.Errorf("copies=%d should be rejected", bad)
		}
	}
}

func TestValidatePrinterName(t *testing.T) {
	if name, err := validatePrinterName("op", map[string]any{}); err != nil || name != "" {
		t.Errorf("absent printer should be empty (default), got %q, %v", name, err)
	}
	if name, err := validatePrinterName("op", map[string]any{"printer": " Office "}); err != nil || name != "Office" {
		t.Errorf("printer should trim to Office, got %q, %v", name, err)
	}
	for _, bad := range []string{"-d", "Bad\tName"} {
		if _, err := validatePrinterName("op", map[string]any{"printer": bad}); err == nil {
			t.Errorf("printer %q should be rejected", bad)
		}
	}
}

func TestStagePrintFile_BuildsPlan(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	plan, err := stagePrintFile(context.Background(), lookupCapability(t, "print_file"), map[string]any{
		"file": file, "printer": "Office", "copies": 2,
	})
	if err != nil {
		t.Fatalf("stagePrintFile: %v", err)
	}
	if plan.Inverse != nil {
		t.Error("printing is irreversible: Inverse should be nil")
	}
	want := []string{"-d", "Office", "-n", "2", "--", file}
	if plan.Forward.Binary != "lp" || !reflect.DeepEqual(plan.Forward.Args, want) {
		t.Errorf("Forward = %s %v, want lp %v", plan.Forward.Binary, plan.Forward.Args, want)
	}
}

func TestStagePrintFile_RejectsMissingFile(t *testing.T) {
	if _, err := stagePrintFile(context.Background(), lookupCapability(t, "print_file"), map[string]any{
		"file": filepath.Join(t.TempDir(), "nope.pdf"),
	}); err == nil {
		t.Error("a nonexistent file should be rejected at stage time")
	}
}

func TestStagePrintTestPage_WritesScratchAndStages(t *testing.T) {
	plan, err := stagePrintTestPage(context.Background(), lookupCapability(t, "print_test_page"), map[string]any{})
	if err != nil {
		t.Fatalf("stagePrintTestPage: %v", err)
	}
	scratch := filepath.Join(fallbackDir, "testpage.txt")
	if _, err := os.Stat(scratch); err != nil {
		t.Errorf("test page scratch file should exist after staging: %v", err)
	}
	want := []string{"--", scratch}
	if plan.Forward.Binary != "lp" || !reflect.DeepEqual(plan.Forward.Args, want) {
		t.Errorf("Forward = %s %v, want lp %v", plan.Forward.Binary, plan.Forward.Args, want)
	}
}
