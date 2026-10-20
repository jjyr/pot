package control

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestManageScheduleToolControlSystemTimezone(t *testing.T) {
	t.Setenv("TZ", "Europe/Berlin")
	s, _ := batchService(t, func(http.ResponseWriter, *http.Request) { t.Error("schedule management must not call a model") }, 1)
	for _, test := range []struct{ id, zone, want string }{{"system", "", "Europe/Berlin"}, {"override", "America/New_York", "America/New_York"}} {
		spec := map[string]any{"title": "Cron fixture", "goal": "Fixture only", "kind": "cron", "cron": "0 8-20 * * *", "enabled": false}
		if test.zone != "" {
			spec["timezone"] = test.zone
		}
		args, _ := json.Marshal(map[string]any{"action": "create", "spec": spec})
		reply, err := s.productCall(context.Background(), "main", test.id, "manage_schedule", args)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(reply)
		if err != nil {
			t.Fatal(err)
		}
		var info protocol.ScheduleInfo
		if err = json.Unmarshal(raw, &info); err != nil {
			t.Fatal(err)
		}
		if info.Spec.Timezone != test.want || info.Spec.Kind != "cron" || info.Spec.Cron != "0 8-20 * * *" {
			t.Fatal(string(raw))
		}
	}
}
