// mutate_reminders_test.go tests the Reminders mutators' validation, argv
// construction, and preview text.
//
// SAFETY: as in mutate_calendar_test.go, no test executes a StagedPlan, so no
// real reminder is created, modified, completed, or deleted. Full coverage is
// on stageAddReminder (no live probe); modify/complete/delete are covered only
// on their pre-probe validation paths.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

func addReminderCapability(t *testing.T) registry.Capability {
	return lookupCapability(t, "add_reminder")
}

func TestStageAddReminder_TimedDue(t *testing.T) {
	plan, err := stageAddReminder(context.Background(), addReminderCapability(t), map[string]any{
		"list": "Reminders", "title": "Call dentist",
		"due_date": "2026-06-20", "due_time": "09:30", "notes": "ask about Friday",
	})
	if err != nil {
		t.Fatalf("stageAddReminder: %v", err)
	}
	a := plan.Forward.Args
	// ["-e", createReminderScript, "--", list, title, notes, dueMode, y,m,d,h,mn, completed]
	if len(a) != 13 {
		t.Fatalf("forward argv length = %d, want 13: %v", len(a), a)
	}
	if a[1] != createReminderScript || a[2] != "--" {
		t.Errorf("forward should run the create script through the hardened path: %v", a[:3])
	}
	if a[3] != "Reminders" || a[4] != "Call dentist" || a[5] != "ask about Friday" {
		t.Errorf("forward data fields misplaced: %v", a[3:6])
	}
	if a[6] != "timed" {
		t.Errorf("dueMode = %q, want timed: %v", a[6], a)
	}
	if a[7] != "2026" || a[8] != "6" || a[9] != "20" || a[10] != "9" || a[11] != "30" {
		t.Errorf("due components wrong: %v", a[7:12])
	}
	if a[12] != "false" {
		t.Errorf("a new reminder must be created incomplete, got completed=%q", a[12])
	}

	if plan.Inverse == nil || plan.Inverse.Args[1] != deleteReminderByKeyScript {
		t.Fatalf("inverse should delete the created reminder by natural key: %+v", plan.Inverse)
	}
	if plan.Inverse.Args[3] != "Reminders" || plan.Inverse.Args[4] != "Call dentist" {
		t.Errorf("inverse natural key wrong: %v", plan.Inverse.Args[3:])
	}
	for _, want := range []string{"Call dentist", "Reminders", "2026-06-20 09:30", "Undo will delete"} {
		if !strings.Contains(plan.Preview, want) {
			t.Errorf("preview missing %q: %s", want, plan.Preview)
		}
	}
}

func TestStageAddReminder_AllDayDue(t *testing.T) {
	plan, err := stageAddReminder(context.Background(), addReminderCapability(t), map[string]any{
		"list": "Reminders", "title": "Pay rent", "due_date": "2026-07-01",
	})
	if err != nil {
		t.Fatalf("stageAddReminder: %v", err)
	}
	a := plan.Forward.Args
	if a[6] != "allday" {
		t.Errorf("dueMode = %q, want allday: %v", a[6], a)
	}
	if !strings.Contains(plan.Preview, "all day") {
		t.Errorf("preview should note an all-day due date: %s", plan.Preview)
	}
}

func TestStageAddReminder_NoDue(t *testing.T) {
	plan, err := stageAddReminder(context.Background(), addReminderCapability(t), map[string]any{
		"list": "Reminders", "title": "Someday idea",
	})
	if err != nil {
		t.Fatalf("stageAddReminder: %v", err)
	}
	if plan.Forward.Args[6] != "none" {
		t.Errorf("dueMode = %q, want none", plan.Forward.Args[6])
	}
}

func TestStageAddReminder_Rejects(t *testing.T) {
	cases := map[string]map[string]any{
		"missing list":              {"title": "x"},
		"missing title":             {"list": "Reminders"},
		"due_time without due_date": {"list": "Reminders", "title": "x", "due_time": "09:00"},
		"bad due_date":              {"list": "Reminders", "title": "x", "due_date": "2026-02-30"},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := stageAddReminder(context.Background(), addReminderCapability(t), params); err == nil {
				t.Errorf("expected an error for %s", name)
			}
		})
	}
}

func TestStageReminderMutators_RejectMissingParams(t *testing.T) {
	modify := lookupCapability(t, "modify_reminder")
	complete := lookupCapability(t, "complete_reminder")
	del := lookupCapability(t, "delete_reminder")

	if _, err := stageModifyReminder(context.Background(), modify, map[string]any{"list": "Reminders", "id": "R1"}); err == nil {
		t.Error("modify_reminder should require a title")
	}
	if _, err := stageCompleteReminder(context.Background(), complete, map[string]any{"list": "Reminders"}); err == nil {
		t.Error("complete_reminder should require an id")
	}
	if _, err := stageDeleteReminder(context.Background(), del, map[string]any{"id": "R1"}); err == nil {
		t.Error("delete_reminder should require a list")
	}
}
