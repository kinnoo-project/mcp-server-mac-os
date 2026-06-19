// mutate_calendar_test.go tests the Calendar mutators' validation, argv
// construction, and preview text.
//
// SAFETY: no test here ever calls RunCommand on a StagedPlan, so no real
// Calendar event is ever created, modified, or deleted. Tests cover only the
// operations and code paths that reach a decision WITHOUT a live probe:
// stageAddEvent (which builds its plan purely from inputs) in full, and the
// pre-probe validation/rejection paths of modify/delete. Exercising a valid
// modify/delete would require probing a real event and is left to manual
// smoke-testing through the safe stage→execute→undo flow.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func addEventCapability(t *testing.T) registry.Capability { return lookupCapability(t, "add_event") }

func TestStageAddEvent_BuildsReversiblePlan(t *testing.T) {
	plan, err := stageAddEvent(context.Background(), addEventCapability(t), map[string]any{
		"calendar":   "Work",
		"title":      "Design review",
		"date":       "2026-06-18",
		"start_time": "14:00",
		"end_time":   "15:30",
		"location":   "Room 4",
		"notes":      "bring slides",
	})
	if err != nil {
		t.Fatalf("stageAddEvent: %v", err)
	}

	if plan.Forward.Binary != "osascript" {
		t.Errorf("Forward.Binary = %q, want osascript", plan.Forward.Binary)
	}
	a := plan.Forward.Args
	// ["-e", addEventScript, "--", calendar, title, location, notes, sy,sm,sd,sh,smin, ey,em,ed,eh,emin]
	if len(a) != 17 {
		t.Fatalf("forward argv length = %d, want 17: %v", len(a), a)
	}
	if a[0] != "-e" || a[1] != addEventScript {
		t.Errorf("forward should run the add-event script: %v", a[:2])
	}
	if a[2] != "--" {
		t.Errorf("forward missing osascript terminator at index 2: %v", a)
	}
	if a[3] != "Work" || a[4] != "Design review" || a[5] != "Room 4" || a[6] != "bring slides" {
		t.Errorf("forward data fields misplaced: %v", a[3:7])
	}
	// start components 2026-06-18 14:00, end 2026-06-18 15:30.
	if a[7] != "2026" || a[8] != "6" || a[9] != "18" || a[10] != "14" || a[11] != "0" {
		t.Errorf("forward start components wrong: %v", a[7:12])
	}
	if a[15] != "15" || a[16] != "30" {
		t.Errorf("forward end time components wrong: %v", a[12:])
	}

	if plan.Inverse == nil {
		t.Fatal("add_event must be reversible: Inverse should be non-nil")
	}
	inv := *plan.Inverse
	if inv.Args[1] != deleteEventByKeyScript || inv.Args[2] != "--" {
		t.Errorf("inverse should delete by natural key through the hardened path: %v", inv.Args[:3])
	}
	if inv.Args[3] != "Work" || inv.Args[4] != "Design review" {
		t.Errorf("inverse natural key wrong: %v", inv.Args[3:5])
	}

	for _, want := range []string{"Design review", "Work", "2026-06-18", "14:00", "15:30", "Room 4", "Undo will delete"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %s", want, plan.Preview)
		}
	}
}

// TestStageAddEvent_FlagLikeTitleStaysData is the option-injection regression
// test for Calendar: a title of "-e" must land in argv as data after the "--"
// terminator, never as an osascript flag.
func TestStageAddEvent_FlagLikeTitleStaysData(t *testing.T) {
	plan, err := stageAddEvent(context.Background(), addEventCapability(t), map[string]any{
		"calendar": "Work", "title": "-e", "date": "2026-06-18",
		"start_time": "09:00", "end_time": "10:00",
	})
	if err != nil {
		t.Fatalf("stageAddEvent: %v", err)
	}
	a := plan.Forward.Args
	if a[2] != "--" || a[4] != "-e" {
		t.Fatalf("flag-like title not neutralized by terminator: %v", a)
	}
}

func TestStageAddEvent_Rejects(t *testing.T) {
	cases := map[string]map[string]any{
		"missing calendar": {"title": "x", "date": "2026-06-18", "start_time": "09:00", "end_time": "10:00"},
		"missing title":    {"calendar": "Work", "date": "2026-06-18", "start_time": "09:00", "end_time": "10:00"},
		"bad date":         {"calendar": "Work", "title": "x", "date": "2026-13-40", "start_time": "09:00", "end_time": "10:00"},
		"bad start_time":   {"calendar": "Work", "title": "x", "date": "2026-06-18", "start_time": "9am", "end_time": "10:00"},
		"end before start": {"calendar": "Work", "title": "x", "date": "2026-06-18", "start_time": "10:00", "end_time": "09:00"},
		"end equals start": {"calendar": "Work", "title": "x", "date": "2026-06-18", "start_time": "10:00", "end_time": "10:00"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := stageAddEvent(context.Background(), addEventCapability(t), params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

// TestStageModifyEvent_RejectsBeforeProbe confirms the validation that runs
// before the live probe refuses bad input, so these cases never reach osascript.
func TestStageModifyEvent_RejectsBeforeProbe(t *testing.T) {
	cap := lookupCapability(t, "modify_event")
	cases := map[string]map[string]any{
		"missing uid":      {"calendar": "Work", "title": "x", "date": "2026-06-18", "start_time": "09:00", "end_time": "10:00"},
		"missing title":    {"calendar": "Work", "uid": "U1", "date": "2026-06-18", "start_time": "09:00", "end_time": "10:00"},
		"end before start": {"calendar": "Work", "uid": "U1", "title": "x", "date": "2026-06-18", "start_time": "10:00", "end_time": "09:00"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := stageModifyEvent(context.Background(), cap, params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

func TestStageDeleteEvent_RejectsMissingParams(t *testing.T) {
	cap := lookupCapability(t, "delete_event")
	if _, err := stageDeleteEvent(context.Background(), cap, map[string]any{"calendar": "Work"}); err == nil {
		t.Error("expected an error when uid is missing")
	}
	if _, err := stageDeleteEvent(context.Background(), cap, map[string]any{"uid": "U1"}); err == nil {
		t.Error("expected an error when calendar is missing")
	}
}
