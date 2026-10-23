package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestScheduleGoalAndTitleLimits(t *testing.T) {
	const limit = 524288 // 512 KiB, independent of the implementation constant.
	for _, test := range []struct {
		name, title, goal, want string
	}{
		{"Chinese above old limit", "检查", strings.Repeat("汉", 2667), ""},
		{"exact ASCII", "Check", strings.Repeat("a", limit), ""},
		{"ASCII over", "Check", strings.Repeat("a", limit+1), "spec.goal exceeds limit: actual=524289 UTF-8 bytes, limit=524288 UTF-8 bytes (512 KiB)"},
		{"exact Chinese", "检查", strings.Repeat("汉", limit/3) + "ab", ""},
		{"Chinese over", "检查", strings.Repeat("汉", limit/3+1), "spec.goal exceeds limit: actual=524289 UTF-8 bytes, limit=524288 UTF-8 bytes (512 KiB)"},
		{"exact emoji", "Check", strings.Repeat("😀", limit/4), ""},
		{"emoji over", "Check", strings.Repeat("😀", limit/4) + "a", "spec.goal exceeds limit: actual=524289 UTF-8 bytes, limit=524288 UTF-8 bytes (512 KiB)"},
		{"empty goal", "Check", "", "spec.goal must not be empty or whitespace-only"},
		{"blank goal", "Check", " \n\t　", "spec.goal must not be empty or whitespace-only"},
		{"empty title", "", "Check", "spec.title must not be empty or whitespace-only"},
		{"blank title", "　\t", "Check", "spec.title must not be empty or whitespace-only"},
		{"80 rune title", strings.Repeat("😀", 80), "Check", ""},
		{"81 rune title", strings.Repeat("😀", 81), "Check", "spec.title exceeds limit: actual=81 characters, limit=80 characters"},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := defaultSpec()
			spec.Title, spec.Goal = test.title, test.goal
			err := Validate(spec)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != test.want {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestScheduleFullGoalCreateUpdateReadAndDispatch(t *testing.T) {
	for _, goal := range []string{
		strings.Repeat("汉", 2667),
		strings.Repeat("汉", 524288/3) + "ab",
		// Control characters expand sixfold in serialized launch snapshots.
		"a" + strings.Repeat("\x00", 524287),
	} {
		t.Run(fmt.Sprintf("bytes=%d/escaped=%t", len(goal), strings.ContainsRune(goal, 0)), func(t *testing.T) {
			ctx := context.Background()
			f := setup(t)
			spec := defaultSpec()
			spec.Title = strings.Repeat("😀", 80)
			spec.Goal = goal
			info := create(t, f, spec, 0)
			for _, action := range []string{"create", "update"} {
				if action == "update" {
					// Keep the same byte count while proving the new content was saved.
					spec.Goal = "b" + spec.Goal[1:]
					if strings.HasPrefix(goal, "汉") {
						spec.Goal = "字" + goal[len("汉"):]
					}
					if _, err := f.schedule.Manage(ctx, "assistant", Request{RequestID: tasks.ID(), Action: action, ScheduleID: info.ID, Spec: &spec}, 0); err != nil {
						t.Fatal(err)
					}
				}
				plan, err := f.schedule.Get(ctx, "assistant", info.ID)
				if err != nil || plan == nil {
					t.Fatalf("read: plan=%v err=%v", plan != nil, err)
				}
				if plan.Info.Spec.Goal != spec.Goal {
					t.Fatal("persisted goal changed")
				}
				tooLarge := spec
				tooLarge.Goal = strings.Repeat("a", 524289)
				if _, err := f.schedule.Manage(ctx, "assistant", Request{RequestID: tasks.ID(), Action: action, ScheduleID: info.ID, Spec: &tooLarge}, 0); err == nil || !strings.Contains(err.Error(), "spec.goal exceeds limit: actual=524289 UTF-8 bytes, limit=524288 UTF-8 bytes") {
					t.Fatalf("oversize %s: %v", action, err)
				}
			}
			if err := f.schedule.Tick(ctx, 60000); err != nil {
				t.Fatal(err)
			}
			if err := f.tasks.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if len(f.destination.runs) != 1 {
				t.Fatalf("launched %d runs", len(f.destination.runs))
			}
			for _, run := range f.destination.runs {
				if err := run.Launch.Validate("machine"); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(run.Launch.Text, spec.Goal) {
					t.Fatal("launch did not retain full goal")
				}
				record, err := f.tasks.Get(ctx, "assistant", run.Launch.TaskID)
				if err != nil || record == nil || record.Launch == nil || record.Launch.Text != run.Launch.Text {
					t.Fatalf("task readback: %v", err)
				}
			}
			// Persisted-definition reads enforce the same upper boundary.
			plan, err := f.schedule.Get(ctx, "assistant", info.ID)
			if err != nil {
				t.Fatal(err)
			}
			plan.Info.Spec.Goal = strings.Repeat("a", 524289)
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.tasks.DB().ExecContext(ctx, "UPDATE schedules SET body=? WHERE id=?", string(raw), info.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.schedule.Get(ctx, "assistant", info.ID); err == nil || !strings.Contains(err.Error(), "spec.goal exceeds limit") {
				t.Fatalf("oversize persisted goal: %v", err)
			}
		})
	}
}

func TestScheduledCapacityDoesNotWidenOrdinaryTaskAdmission(t *testing.T) {
	f := setup(t)
	request := protocol.CreateTask{RequestID: "large", Text: strings.Repeat("a", 65537)}
	if _, err := f.tasks.Prepare(context.Background(), "assistant", request); err == nil {
		t.Fatal("ordinary task preparation accepted oversize text")
	}
	if _, err := f.tasks.AcceptLocal(context.Background(), "assistant", request); err == nil {
		t.Fatal("ordinary task submission accepted oversize text")
	}
}
