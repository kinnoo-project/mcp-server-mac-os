// menu_test.go guards the domain-tool description rendering, in particular the
// truncation-resilience property added after a long filesystem description was
// clipped mid-list and hid the `move`/`remove` operations from the model.
package server

import (
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestDomainDescription_ListsEveryOperationUpFront asserts that, for every
// category in the real registry, the description leads with a single "All
// operations:" line naming every operation. That line is the part that must
// survive even if a client clips the longer per-operation detail that follows —
// so it is checked to appear BEFORE the detailed "Details" section.
func TestDomainDescription_ListsEveryOperationUpFront(t *testing.T) {
	reg, err := registry.Load()
	if err != nil {
		t.Fatalf("registry.Load(): %v", err)
	}

	seen := map[string]bool{}
	for _, c := range reg.All() {
		if seen[c.Category] {
			continue
		}
		seen[c.Category] = true

		caps := reg.List(c.Category)
		desc := domainToolDescription(c.Category, caps)

		allIdx := strings.Index(desc, "All operations: ")
		detailIdx := strings.Index(desc, "\n\nDetails")
		if allIdx == -1 {
			t.Errorf("%s: description has no 'All operations:' summary line", c.Category)
			continue
		}
		if detailIdx != -1 && allIdx > detailIdx {
			t.Errorf("%s: 'All operations:' must precede the 'Details' section", c.Category)
		}

		// Parse the "All operations:" line into exact comma-separated tokens and
		// check membership by equality. strings.Contains on the full description
		// produces false positives when one operation name is a substring of
		// another (e.g. "move" is contained in "remove"), which would let a
		// genuinely missing operation silently pass the test.
		allOpsStart := allIdx + len("All operations: ")
		lineEnd := strings.Index(desc[allOpsStart:], "\n")
		allOpsLine := desc[allOpsStart:]
		if lineEnd != -1 {
			allOpsLine = desc[allOpsStart : allOpsStart+lineEnd]
		}
		listedOps := map[string]bool{}
		for _, tok := range strings.Split(allOpsLine, ", ") {
			listedOps[strings.TrimSpace(tok)] = true
		}
		for _, op := range caps {
			if !listedOps[op.Name] {
				t.Errorf("%s: operation %q missing from the up-front 'All operations:' list", c.Category, op.Name)
			}
		}
	}
}

// TestRenderParams_MarksRequiredCompactly confirms required parameters are
// flagged with a "*" suffix (the compact marker) rather than the older verbose
// ", required" phrasing, and that optional parameters carry no marker.
func TestRenderParams_MarksRequiredCompactly(t *testing.T) {
	params := []registry.ParamSpec{
		{Name: "must", Type: registry.TypeString, Required: true, Description: "needed"},
		{Name: "maybe", Type: registry.TypeString, Required: false, Description: "optional"},
	}
	got := renderParams(params)

	if !strings.Contains(got, "must* (string)") {
		t.Errorf("required param not marked with '*': %q", got)
	}
	if strings.Contains(got, ", required") {
		t.Errorf("verbose ', required' phrasing should be gone: %q", got)
	}
	if !strings.Contains(got, "maybe (string)") || strings.Contains(got, "maybe* (string)") {
		t.Errorf("optional param should carry no '*': %q", got)
	}
}
