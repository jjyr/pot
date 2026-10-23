package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func millis(value string) int64 {
	instant, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return instant.UnixMilli()
}
func defaultSpec() protocol.ScheduleSpec {
	return protocol.ScheduleSpec{MachineID: "machine", Title: "Monitor", Goal: "Check source", Timezone: "Asia/Shanghai", Kind: "interval", EveryMinutes: 1, Misfire: "coalesce", Notify: "all", Enabled: true}
}
func TestCalendarDSTEarlierOccurrenceGapsAndWeekdays(t *testing.T) {
	for _, test := range []struct{ kind, zone, clock, at, after, want string }{{"cron", "America/New_York", "02:30", "", "2026-03-07T08:00:00Z", "2026-03-09T06:30:00Z"}, {"cron", "America/New_York", "01:30", "", "2026-11-01T05:00:00Z", "2026-11-01T05:30:00Z"}, {"cron", "America/New_York", "01:30", "", "2026-11-01T05:31:00Z", "2026-11-02T06:30:00Z"}, {"once", "America/New_York", "", "2026-11-01 01:30", "2026-10-31T00:00:00Z", "2026-11-01T05:30:00Z"}, {"cron", "Asia/Shanghai", "09:00", "", "2026-10-16T02:00:00Z", "2026-10-19T01:00:00Z"}} {
		spec := defaultSpec()
		spec.Kind = test.kind
		spec.Timezone = test.zone
		spec.Cron = map[string]string{"02:30": "30 2 * * *", "01:30": "30 1 * * *", "09:00": "0 9 * * MON"}[test.clock]
		spec.At = test.at

		due, err := Next(spec, millis(test.after))
		if err != nil || due == nil || *due != millis(test.want) {
			t.Fatalf("%+v due=%v err=%v", test, due, err)
		}
	}
	spec := defaultSpec()
	spec.Kind = "once"
	spec.Timezone = "America/New_York"
	spec.At = "2026-03-08 02:30"
	if Validate(spec) == nil {
		t.Fatal("nonexistent one-off accepted")
	}
	spec = defaultSpec()
	next, err := AfterDue(spec, 100000, 290000)
	if err != nil || next == nil || *next != 340000 {
		t.Fatal(next, err)
	}
}

type destination struct {
	runs     map[string]tasks.Run
	launches int
}

func (d *destination) Launch(_ context.Context, l tasks.Launch) (tasks.Run, error) {
	hash, _ := l.Digest()
	r := tasks.Run{Launch: l, LaunchDigest: hash, Phase: tasks.Running, BootID: "boot", StartedAtMS: time.Now().UnixMilli()}
	d.runs[l.RunID] = r
	d.launches++
	return r, nil
}
func (d *destination) Inspect(_ context.Context, control, task, run string) (*tasks.Run, error) {
	r, ok := d.runs[run]
	if !ok {
		return nil, nil
	}
	return &r, nil
}
func (d *destination) Stop(context.Context, string, string, string, string) error { return nil }
func (d *destination) complete(task string) {
	for id, r := range d.runs {
		if r.Launch.TaskID == task {
			r.Phase = tasks.Complete
			r.Result = &tasks.Terminal{Outcome: core.Success, Text: "observed"}
			r.CleanupConfirmed = true
			d.runs[id] = r
		}
	}
}

func (d *destination) Resume(_ context.Context, node, task, run string) error {
	r, ok := d.runs[run]
	if !ok || r.Launch.ControlID != node || r.Launch.TaskID != task {
		return fmt.Errorf("original run unavailable")
	}
	r.Phase = tasks.Accepted
	d.runs[run] = r
	return nil
}

type fixture struct {
	schedule    *Service
	tasks       *tasks.Service
	destination *destination
	ready       *bool
	modelReads  *int
	events      *[]hooks.Event
}

func setup(t *testing.T) fixture {
	t.Helper()
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	settings.BuiltinTools = []string{}
	profile := core.Profile{ID: "profile", Revision: 1, Name: "test", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	spec, err := profile.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	port := &destination{runs: map[string]tasks.Run{}}
	reads := 0
	var readsMu sync.Mutex
	var service *Service
	var events []hooks.Event
	queue, err := tasks.Open(filepath.Join(t.TempDir(), "tasks.sqlite"), tasks.Options{NodeID: "machine", Executor: port, PublishHook: func(event hooks.Event) { events = append(events, event) }, PrepareOutcome: func(ctx context.Context, record tasks.Record, event protocol.TaskResultEvent) (tasks.OutcomeCommit, error) {
		return service.PrepareOutcome(ctx, record, event)
	}, ResolveModel: func(context.Context, string, protocol.CreateTask) (tasks.Selection, error) {
		readsMu.Lock()
		reads++
		readsMu.Unlock()
		return tasks.Selection{Config: spec, Plugins: []string{}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { queue.Close() })
	ready := true
	service, err = New(Options{Tasks: queue, Ready: func(context.Context, string) (bool, error) { return ready, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{service, queue, port, &ready, &reads, &events}
}
func create(t *testing.T, f fixture, spec protocol.ScheduleSpec, at int64) protocol.ScheduleInfo {
	t.Helper()
	raw, err := f.schedule.Manage(context.Background(), "assistant", Request{RequestID: tasks.ID(), Action: "create", Spec: &spec}, at)
	if err != nil {
		t.Fatal(err)
	}
	var info protocol.ScheduleInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err, string(raw))
	}
	return info
}
func TestReadinessCoalescingSkipAndOverlap(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	at := millis("2026-10-16T00:00:00Z")
	info := create(t, f, defaultSpec(), at)
	*f.ready = false
	if err := f.schedule.Tick(ctx, at+180000); err != nil {
		t.Fatal(err)
	}
	page, _ := f.tasks.Page(ctx, "assistant", protocol.TaskQuery{Scope: "all"})
	if page.Total != 0 {
		t.Fatal("unready schedule created task")
	}
	*f.ready = true
	if err := f.schedule.Tick(ctx, at+190000); err != nil {
		t.Fatal(err)
	}
	page, _ = f.tasks.Page(ctx, "assistant", protocol.TaskQuery{Scope: "all"})
	if page.Total != 1 {
		t.Fatal(page)
	}
	plan, _ := f.schedule.Get(ctx, "assistant", info.ID)
	if plan.Info.NextAtMS == nil || *plan.Info.NextAtMS != at+240000 {
		t.Fatal(plan)
	}
	if err := f.schedule.Tick(ctx, at+240000); err != nil {
		t.Fatal(err)
	}
	history, err := f.schedule.Page(ctx, "assistant", Query{Selected: info.ID})
	if err != nil || len(history.Runs) != 2 || history.Runs[0].State != "skipped_overlap" {
		t.Fatal(history, err)
	}
	if *f.modelReads != 2 {
		t.Fatal("overlap resolved model selection", *f.modelReads)
	}
	skip := defaultSpec()
	skip.Misfire = "skip"
	skipInfo := create(t, f, skip, at)
	before := *f.modelReads
	if err := f.schedule.Tick(ctx, at+120000); err != nil {
		t.Fatal(err)
	}
	history, err = f.schedule.Page(ctx, "assistant", Query{Selected: skipInfo.ID})
	if err != nil || len(history.Runs) != 1 || history.Runs[0].State != "skipped_missed" || *f.modelReads != before {
		t.Fatal(history, *f.modelReads, err)
	}
}
func TestEachScheduledCompletionUsesFinalReply(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	at := millis("2026-10-16T00:00:00Z")
	info := create(t, f, defaultSpec(), at)
	for i := int64(1); i <= 2; i++ {
		if err := f.schedule.Tick(ctx, at+i*60000); err != nil {
			t.Fatal(err)
		}
		if err := f.tasks.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		history, _ := f.schedule.Page(ctx, "assistant", Query{Selected: info.ID})
		task := *history.Runs[0].TaskID
		record, _ := f.tasks.Get(ctx, "assistant", task)
		if strings.Contains(record.Request.Text, "fingerprint") || strings.Contains(record.Request.Text, ".pot-schedule-result.json") {
			t.Fatal(record.Request.Text)
		}
		f.destination.complete(task)
		if err := f.tasks.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := f.tasks.Notifications(ctx, "assistant")
		if err != nil || len(notices) != 1 || notices[0].Text != "observed" {
			t.Fatal(notices, err)
		}
		if err := f.tasks.AcknowledgeNotification(ctx, "assistant", notices[0].ID); err != nil {
			t.Fatal(err)
		}
	}
}
func TestOccurrenceAndQueueRollbackTogetherAndWritesAreIdempotent(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	at := millis("2026-10-16T00:00:00Z")
	spec := defaultSpec()
	request := Request{RequestID: "create", Action: "create", Spec: &spec}
	first, err := f.schedule.Manage(ctx, "assistant", request, at)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.schedule.Manage(ctx, "assistant", request, at+10)
	if err != nil || string(first) != string(again) {
		t.Fatal(string(again), err)
	}
	spec.Goal = "conflict"
	if _, err := f.schedule.Manage(ctx, "assistant", request, at); err == nil {
		t.Fatal("changed request accepted")
	}
	var info protocol.ScheduleInfo
	_ = json.Unmarshal(first, &info)
	if _, err := f.tasks.DB().Exec(`CREATE TRIGGER fail_occurrence BEFORE INSERT ON schedule_runs BEGIN SELECT RAISE(ABORT,'fixture failure');END`); err != nil {
		t.Fatal(err)
	}
	if err := f.schedule.Tick(ctx, at+60000); err == nil {
		t.Fatal("failed occurrence accepted")
	}
	page, _ := f.tasks.Page(ctx, "assistant", protocol.TaskQuery{Scope: "all"})
	plan, _ := f.schedule.Get(ctx, "assistant", info.ID)
	if page.Total != 0 || plan.Info.NextAtMS == nil || *plan.Info.NextAtMS != at+60000 {
		t.Fatal(page, plan)
	}
	if _, err := f.tasks.DB().Exec("DROP TRIGGER fail_occurrence"); err != nil {
		t.Fatal(err)
	}
	if err := f.schedule.Tick(ctx, at+60000); err != nil {
		t.Fatal(err)
	}
	if err := f.schedule.Tick(ctx, at+60000); err != nil {
		t.Fatal(err)
	}
	page, _ = f.tasks.Page(ctx, "assistant", protocol.TaskQuery{Scope: "all"})
	if page.Total != 1 {
		t.Fatal(page)
	}
}
func TestHistoryRetentionKeepsUnfinished(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	at := millis("2026-10-16T00:00:00Z")
	info := create(t, f, defaultSpec(), at)
	err := storage.Transaction(ctx, f.tasks.DB(), func(tx *sql.Tx) error {
		for i := range 105 {
			finished := at + int64(i)
			run := protocol.ScheduleRun{ID: fmt.Sprint(i), ScheduledForMS: finished, State: "skipped", FinishedAtMS: &finished, Notify: "all"}
			if err := writeRun(ctx, tx, "assistant", info.ID, run); err != nil {
				return err
			}
		}
		for i := range 2 {
			run := protocol.ScheduleRun{ID: fmt.Sprintf("unfinished%d", i), ScheduledForMS: at, State: "queued", Notify: "all"}
			if err := writeRun(ctx, tx, "assistant", info.ID, run); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var ended, unfinished int
	if err := f.tasks.DB().QueryRow("SELECT COUNT(*) FROM schedule_runs WHERE finished IS NOT NULL").Scan(&ended); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.DB().QueryRow("SELECT COUNT(*) FROM schedule_runs WHERE finished IS NULL").Scan(&unfinished); err != nil {
		t.Fatal(err)
	}
	if ended != 100 || unfinished != 2 {
		t.Fatal(ended, unfinished)
	}
}
