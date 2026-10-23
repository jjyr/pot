package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestManageScheduleToolFullGoal(t *testing.T) {
	s, _ := batchService(t, func(http.ResponseWriter, *http.Request) { t.Error("schedule management called a model") }, 1)
	ctx := context.Background()
	dispatch := s.productDispatch("main", "schedule-goal", 1, false)
	goal := "a" + strings.Repeat("\x00", 524287)
	spec := map[string]any{"title": strings.Repeat("😀", 80), "goal": goal, "kind": "interval", "every_minutes": 1, "timezone": "UTC", "enabled": false}
	id := ""
	for _, action := range []string{"create", "update"} {
		args, err := json.Marshal(map[string]any{"action": action, "schedule_id": id, "spec": spec})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := dispatch["manage_schedule"](ctx, core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: action, Name: "manage_schedule", Arguments: args}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var info protocol.ScheduleInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatal(err)
		}
		id = info.ID
		if info.Spec.Goal != spec["goal"] {
			t.Fatal("tool reply lost goal bytes")
		}
		plan, err := s.Schedules.Get(ctx, "main", id)
		if err != nil || plan == nil || plan.Info.Spec.Goal != spec["goal"] {
			t.Fatalf("tool persisted goal incorrectly: %v", err)
		}
		spec["goal"] = "b" + goal[1:]
	}
	spec["goal"] = strings.Repeat("汉", 524288/3+1)
	for _, action := range []string{"create", "update"} {
		args, err := json.Marshal(map[string]any{"action": action, "schedule_id": id, "spec": spec})
		if err != nil {
			t.Fatal(err)
		}
		_, err = dispatch["manage_schedule"](ctx, core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "oversize-" + action, Name: "manage_schedule", Arguments: args}, nil)
		if err == nil || !strings.Contains(err.Error(), "spec.goal exceeds limit: actual=524289 UTF-8 bytes, limit=524288 UTF-8 bytes") {
			t.Fatalf("oversize tool %s: %v", action, err)
		}
	}
}
