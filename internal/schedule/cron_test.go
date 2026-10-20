package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

func cronSpec(rule, zone string) protocol.ScheduleSpec {
	spec := defaultSpec()
	spec.Kind = "cron"
	spec.Cron = rule
	spec.Timezone = zone
	return spec
}
func TestCronCalendar(t *testing.T) {
	for _, test := range []struct{ rule, zone, after, want string }{
		{"0 8-20 * * *", "Asia/Tokyo", "2026-10-01T11:00:00Z", "2026-10-01T23:00:00Z"},
		{"0 8-20 * * *", "America/New_York", "2026-10-01T11:00:00Z", "2026-10-01T12:00:00Z"},
		{"*/15 8-9 * JAN MON-FRI", "UTC", "2027-01-01T08:00:00Z", "2027-01-01T08:15:00Z"},
		{"0,30 8,20 * * SAT,SUN", "UTC", "2026-10-02T21:00:00Z", "2026-10-03T08:00:00Z"},
		{"0 8 1 * MON", "UTC", "2026-10-01T08:00:00Z", "2026-10-05T08:00:00Z"},
		{"0 8 1 * *", "UTC", "2026-10-01T08:00:00Z", "2026-11-01T08:00:00Z"},
		{"0 0 29 FEB *", "UTC", "2026-03-01T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"0 0 29 FEB *", "UTC", "2096-02-29T00:00:00Z", "2104-02-29T00:00:00Z"},
		{"30 2 * * *", "America/New_York", "2026-03-07T08:00:00Z", "2026-03-09T06:30:00Z"},
		{"30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-02T06:30:00Z"},
		{"45 1 * * *", "Australia/Lord_Howe", "2026-04-04T14:45:00Z", "2026-04-05T15:15:00Z"},
		{"15 2 * * *", "Australia/Lord_Howe", "2026-10-03T00:00:00Z", "2026-10-04T15:15:00Z"},
	} {
		due, err := Next(cronSpec(test.rule, test.zone), millis(test.after))
		if err != nil || due == nil || *due != millis(test.want) {
			t.Fatalf("%+v next=%v err=%v", test, due, err)
		}
	}
	after := millis("2026-10-01T00:00:00Z")
	spec := cronSpec("0 8-20 * * *", "UTC")
	for hour := 8; hour <= 20; hour++ {
		due, err := Next(spec, after)
		if err != nil || due == nil || time.UnixMilli(*due).UTC().Hour() != hour {
			t.Fatal(hour, due, err)
		}
		after = *due
	}
	due, err := Next(spec, after)
	if err != nil || *due != millis("2026-10-02T08:00:00Z") {
		t.Fatal(due, err)
	}
	if _, err := Next(cronSpec("0 0 31 FEB *", "UTC"), after); err == nil {
		t.Fatal("impossible date accepted")
	}
	result := Preview(PreviewRequest{Kind: "cron", Cron: spec.Cron, Timezone: spec.Timezone}, after)
	for _, instant := range result.Times {
		want, err := Next(spec, after)
		if err != nil || *want != instant {
			t.Fatal(result, err)
		}
		after = instant
	}
	if result.Field != "" || len(result.Times) != 5 {
		t.Fatal(result)
	}
}
func TestControlTimezoneSnapshotAndOverride(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	zone := "Europe/Berlin"
	calls := 0
	f.schedule.options.SystemTimezone = func() (string, error) {
		calls++
		if zone == "" {
			return "", errors.New("cannot resolve Control zone")
		}
		return zone, nil
	}
	spec := cronSpec("0 8-20 * * *", "")
	spec.MachineID = "machine" // Runtime's zone is not consulted.
	raw, err := f.schedule.Manage(ctx, "assistant", Request{RequestID: "default", Action: "create", Spec: &spec}, millis("2026-10-01T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	var info protocol.ScheduleInfo
	if err = json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if info.Spec.Timezone != "Europe/Berlin" || calls != 1 {
		t.Fatal(info, calls)
	}
	zone = "Asia/Tokyo"
	if _, err = f.schedule.Manage(ctx, "assistant", Request{RequestID: "default", Action: "create", Spec: &spec}, millis("2026-10-01T01:00:00Z")); err != nil || calls != 1 {
		t.Fatal("retry resolved new zone", err, calls)
	}
	saved, err := f.schedule.Get(ctx, "assistant", info.ID)
	if err != nil || saved.Info.Spec.Timezone != "Europe/Berlin" {
		t.Fatal(saved, err)
	}
	_, err = f.schedule.Manage(ctx, "assistant", Request{RequestID: "edit", Action: "update", ScheduleID: info.ID, Spec: &spec}, millis("2026-10-02T00:00:00Z"))
	if err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	saved, err = f.schedule.Get(ctx, "assistant", info.ID)
	if err != nil || saved.Info.Spec.Timezone != "Europe/Berlin" {
		t.Fatal(saved, err)
	}
	spec.Timezone = "America/New_York"
	zone = ""
	if _, err = f.schedule.Manage(ctx, "assistant", Request{RequestID: "explicit", Action: "create", Spec: &spec}, millis("2026-10-01T00:00:00Z")); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	spec.Timezone = ""
	if _, err = f.schedule.Manage(ctx, "assistant", Request{RequestID: "unresolved", Action: "create", Spec: &spec}, millis("2026-10-01T00:00:00Z")); err == nil {
		t.Fatal("unresolved default accepted")
	}
	spec = cronSpec("0 0 31 FEB *", "UTC")
	spec.Enabled = false
	if _, err = f.schedule.Manage(ctx, "assistant", Request{RequestID: "impossible", Action: "create", Spec: &spec}, millis("2026-10-01T00:00:00Z")); err == nil {
		t.Fatal("disabled impossible rule accepted")
	}
}

func TestCronConcurrentOccurrenceRestartPauseAndEdit(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	at := millis("2026-10-01T07:59:00Z")
	spec := cronSpec("0 8-20 * * *", "UTC")
	info := create(t, f, spec, at)
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Go(func() { errs <- f.schedule.Tick(ctx, at+60000) })
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.tasks.Page(ctx, "assistant", protocol.TaskQuery{Scope: "all"})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	task, err := f.tasks.Get(ctx, "assistant", page.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	// A time-only edit fences an already admitted run from overwriting baseline.
	spec.Cron = "30 8-20 * * *"
	if _, err = f.schedule.Manage(ctx, "assistant", Request{RequestID: "edit-cron", Action: "update", ScheduleID: info.ID, Spec: &spec}, at+61000); err != nil {
		t.Fatal(err)
	}
	plan, err := f.schedule.Get(ctx, "assistant", info.ID)
	if err != nil || plan.Generation == 0 || *plan.Info.NextAtMS != millis("2026-10-01T08:30:00Z") {
		t.Fatal(plan, err)
	}
	if task.Origin.Kind != "schedule" {
		t.Fatal(task)
	}
	if err = f.tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	f.destination.complete(task.Info.ID)
	if err = f.tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	plan, err = f.schedule.Get(ctx, "assistant", info.ID)
	if err != nil || plan.Info.Baseline != nil {
		t.Fatal("old run changed baseline", plan, err)
	}
	// Reopen the real task database; the fixed zone and due survive process recovery.
	selection, err := f.tasks.Prepare(ctx, "assistant", protocol.CreateTask{RequestID: "restart-check", Text: "Fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var sequence int
	var database, path string
	if err := f.tasks.DB().QueryRow("PRAGMA database_list").Scan(&sequence, &database, &path); err != nil {
		t.Fatal(err)
	}
	if err = f.tasks.Close(); err != nil {
		t.Fatal(err)
	}
	queue, err := tasks.Open(path, tasks.Options{ControlID: "control", RuntimeID: "machine", Port: f.destination, ResolveModel: func(context.Context, string, protocol.CreateTask) (tasks.Selection, error) { return selection, nil }, ResolveTarget: func(context.Context, string) (core.ExecutionTarget, error) { return core.RuntimeTarget("machine"), nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	service, err := New(Options{Tasks: queue, Ready: func(context.Context, string) (bool, error) { return true, nil }, SystemTimezone: func() (string, error) { return "Asia/Tokyo", nil }})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = service.Get(ctx, "assistant", info.ID)
	if err != nil || plan.Info.Spec.Timezone != "UTC" || *plan.Info.NextAtMS != millis("2026-10-01T08:30:00Z") {
		t.Fatal(plan, err)
	}
	if _, err = service.Manage(ctx, "assistant", Request{RequestID: "pause", Action: "pause", ScheduleID: info.ID}, at+120000); err != nil {
		t.Fatal(err)
	}
	if err = service.Tick(ctx, millis("2026-10-01T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Manage(ctx, "assistant", Request{RequestID: "resume", Action: "resume", ScheduleID: info.ID}, millis("2026-10-01T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	plan, err = service.Get(ctx, "assistant", info.ID)
	if err != nil || *plan.Info.NextAtMS != millis("2026-10-01T10:30:00Z") {
		t.Fatal(plan, err)
	}
}

func TestCronMisfireAndOverlap(t *testing.T) {
	for _, policy := range []string{"coalesce", "skip"} {
		t.Run(policy, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			spec := cronSpec("0 8-20 * * *", "UTC")
			spec.Misfire = policy
			info := create(t, f, spec, millis("2026-10-01T07:00:00Z"))
			if err := f.schedule.Tick(ctx, millis("2026-10-01T10:00:00Z")); err != nil {
				t.Fatal(err)
			}
			plan, err := f.schedule.Get(ctx, "assistant", info.ID)
			if err != nil || *plan.Info.NextAtMS != millis("2026-10-01T11:00:00Z") {
				t.Fatal(plan, err)
			}
			page, err := f.schedule.Page(ctx, "assistant", Query{Selected: info.ID})
			if err != nil || len(page.Runs) != 1 {
				t.Fatal(page, err)
			}
			if policy == "skip" {
				if page.Runs[0].State != "skipped_missed" || page.Runs[0].TaskID != nil {
					t.Fatal(page)
				}
			} else {
				if page.Runs[0].TaskID == nil {
					t.Fatal(page)
				}
				if err = f.schedule.Tick(ctx, millis("2026-10-01T11:00:00Z")); err != nil {
					t.Fatal(err)
				}
				page, err = f.schedule.Page(ctx, "assistant", Query{Selected: info.ID})
				if err != nil || len(page.Runs) != 2 || page.Runs[0].State != "skipped_overlap" {
					t.Fatal(page, err)
				}
			}
		})
	}
}
