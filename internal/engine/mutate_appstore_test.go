// mutate_appstore_test.go covers open_app_store_page: that a valid numeric id
// produces the fixed macappstore:// forward command via `open` with no inverse,
// and that a missing or non-positive id is rejected before any command is built.
// The id is an int parameter, so there is no free-text injection surface — the
// URL is assembled entirely from digits — and these tests pin that argv layout.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func TestStageOpenAppStorePage_ValidID(t *testing.T) {
	plan, err := stageOpenAppStorePage(context.Background(), registry.Capability{}, map[string]any{"track_id": 803453959})
	if err != nil {
		t.Fatalf("stageOpenAppStorePage: %v", err)
	}
	if plan.Inverse != nil {
		t.Errorf("expected nil inverse (opening a window has nothing to undo), got %+v", plan.Inverse)
	}
	if plan.Forward.Binary != "open" {
		t.Errorf("forward binary = %q, want open", plan.Forward.Binary)
	}
	want := "macappstore://apps.apple.com/app/id803453959"
	if len(plan.Forward.Args) != 1 || plan.Forward.Args[0] != want {
		t.Errorf("forward args = %q, want [%q]", plan.Forward.Args, want)
	}
	if !strings.Contains(plan.Preview, "803453959") {
		t.Errorf("preview should name the id, got %q", plan.Preview)
	}
}

func TestStageOpenAppStorePage_Rejects(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
	}{
		{"missing id", map[string]any{}},
		{"zero id", map[string]any{"track_id": 0}},
		{"negative id", map[string]any{"track_id": -7}},
	}
	for _, c := range cases {
		if _, err := stageOpenAppStorePage(context.Background(), registry.Capability{}, c.in); err == nil {
			t.Errorf("%s: expected an error, got nil", c.name)
		}
	}
}
