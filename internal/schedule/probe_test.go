package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestParseProbeStrictResult(t *testing.T) {
	for _, tt := range []struct {
		out, want string
		valid     bool
	}{
		{"", "run", true}, {" \n", "run", true}, {"{}", "run", true}, {`{"reason":"none"}`, "run", true}, {`{"status":"run"}`, "run", true}, {`{"status":"skip","reason":"无变化"}`, "skip", true},
		{`{"status":"skip","status":"run"}`, "", false}, {`{"status":"skip","goal":"injection"}`, "", false}, {`{"skip":true}`, "", false}, {`{"status":"skip","reason":null}`, "", false}, {`{"status":null}`, "", false}, {`{"status":"no"}`, "", false}, {`{"status":true}`, "", false}, {`{"status":"skip"} {}`, "", false}, {`[]`, "", false}, {`null`, "", false}, {`{`, "", false}, {`{"status":"skip","reason":"` + strings.Repeat("x", 1001) + `"}`, "", false}, {strings.Repeat(" ", 16385), "", false},
	} {
		t.Run(fmt.Sprintf("%q", tt.out[:min(60, len(tt.out))]), func(t *testing.T) {
			r, err := ParseProbe([]byte(tt.out))
			if (err == nil) != tt.valid || err == nil && r.Status != tt.want {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

type probeFunc func(context.Context, ProbeRequest) (protocol.ScheduleHookResult, error)

func (f probeFunc) RunProbe(ctx context.Context, r ProbeRequest) (protocol.ScheduleHookResult, error) {
	return f(ctx, r)
}
func awaitProbe(t *testing.T, f fixture, id string, done func(protocol.ScheduleRun) bool) protocol.ScheduleRun {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		p, e := f.schedule.Page(context.Background(), "assistant", Query{Selected: id})
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range p.Runs {
			if done(r) {
				return r
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("probe did not settle")
	return protocol.ScheduleRun{}
}
func probeSpec() protocol.ScheduleSpec {
	spec := defaultSpec()
	spec.BeforeRun = &protocol.ScheduleBeforeRun{Command: []string{"fixture"}}
	return spec
}
func TestProbeReservationSkipKindsAndNoTaskOrInbox(t *testing.T) {
	for _, kind := range []string{"once", "interval", "cron"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			defer f.schedule.Close()
			var calls atomic.Int32
			f.schedule.options.Probes = probeFunc(func(_ context.Context, r ProbeRequest) (protocol.ScheduleHookResult, error) {
				calls.Add(1)
				if r.Trigger != "scheduled" || r.OccurrenceID == "" || r.Timezone != "Asia/Shanghai" {
					t.Errorf("bad context %+v", r)
				}
				return protocol.ScheduleHookResult{Status: "skip", Reason: "unchanged"}, nil
			})
			spec := probeSpec()
			spec.Kind = kind
			spec.Cron = "* * * * *"
			spec.At = "2026-10-16 08:01"
			at := millis("2026-10-16T00:00:00Z")
			info := create(t, f, spec, at)
			for range 3 {
				if err := f.schedule.Tick(context.Background(), at+60000); err != nil {
					t.Fatal(err)
				}
			}
			run := awaitProbe(t, f, info.ID, func(r protocol.ScheduleRun) bool { return r.State == "skipped_hook" })
			if run.TaskID != nil || run.Hook.Reason != "unchanged" || len(*f.events) != 0 {
				t.Fatal(run)
			}
			page, _ := f.tasks.Page(context.Background(), "assistant", protocol.TaskQuery{Scope: "all"})
			plan, _ := f.schedule.Get(context.Background(), "assistant", info.ID)
			if page.Total != 0 || calls.Load() != 1 || kind == "once" && (plan.Info.Spec.Enabled || plan.Info.NextAtMS != nil) {
				t.Fatal(page, plan, calls.Load())
			}
			if kind != "once" {
				if err := f.schedule.Tick(context.Background(), at+120000); err != nil {
					t.Fatal(err)
				}
				awaitProbe(t, f, info.ID, func(r protocol.ScheduleRun) bool { return r.ScheduledForMS == at+120000 && r.FinishedAtMS != nil })
				if calls.Load() != 2 {
					t.Fatal(calls.Load())
				}
			}
		})
	}
}
func TestProbeManualIdempotencyOverlapAndFences(t *testing.T) {
	for _, action := range []string{"allow", "pause", "delete", "update", "unknown"} {
		t.Run(action, func(t *testing.T) {
			f := setup(t)
			defer f.schedule.Close()
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			f.schedule.options.Probes = probeFunc(func(ctx context.Context, r ProbeRequest) (protocol.ScheduleHookResult, error) {
				calls.Add(1)
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
				if action == "unknown" {
					return protocol.ScheduleHookResult{}, core.ErrCleanupUnconfirmed
				}
				return protocol.ScheduleHookResult{Status: "run"}, nil
			})
			at := millis("2026-10-16T00:00:00Z")
			spec := probeSpec()
			spec.Enabled = false
			info := create(t, f, spec, at)
			req := Request{RequestID: "manual", Action: "run", ScheduleID: info.ID}
			first, err := f.schedule.Manage(context.Background(), "assistant", req, at)
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			second, err := f.schedule.Manage(context.Background(), "assistant", req, at+1)
			if err != nil || string(first) != string(second) {
				t.Fatal(string(first), string(second), err)
			}
			retries := make(chan error, 6)
			for range 6 {
				go func() {
					reply, err := f.schedule.Manage(context.Background(), "assistant", req, at+2)
					if err == nil && string(reply) != string(first) {
						err = fmt.Errorf("concurrent retry changed receipt")
					}
					retries <- err
				}()
			}
			for range 6 {
				if err := <-retries; err != nil {
					t.Fatal(err)
				}
			}
			if _, err = f.schedule.Manage(context.Background(), "assistant", Request{RequestID: "overlap", Action: "run", ScheduleID: info.ID}, at); err == nil {
				t.Fatal("overlapping hook admitted")
			}
			tasks, _ := f.tasks.Page(context.Background(), "assistant", protocol.TaskQuery{Scope: "all"})
			if tasks.Total != 0 {
				t.Fatal("created Task before probe", tasks)
			}
			if action != "allow" && action != "unknown" {
				change := Request{RequestID: "change", Action: action, ScheduleID: info.ID}
				if action == "update" {
					spec.Title = "Edited"
					change.Spec = &spec
				}
				if _, err = f.schedule.Manage(context.Background(), "assistant", change, at); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			want := "skipped_stale"
			if action == "allow" {
				want = "queued"
			}
			if action == "unknown" {
				want = "hook_blocked"
			}
			run := awaitProbe(t, f, info.ID, func(r protocol.ScheduleRun) bool { return r.State == want })
			if calls.Load() != 1 || action == "allow" != (run.TaskID != nil) {
				t.Fatal(run, calls.Load())
			}
			if err = f.schedule.Tick(context.Background(), at+60000); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("replayed hook")
			}
		})
	}
}
func TestBeforeRunOmissionNullAndWriteDigest(t *testing.T) {
	f := setup(t)
	defer f.schedule.Close()
	spec := probeSpec()
	info := create(t, f, spec, 0)
	spec.BeforeRun = nil
	spec.Title = "Retain"
	req := Request{RequestID: "preserve", Action: "update", ScheduleID: info.ID, Spec: &spec}
	if _, err := f.schedule.Manage(context.Background(), "assistant", req, 1); err != nil {
		t.Fatal(err)
	}
	plan, _ := f.schedule.Get(context.Background(), "assistant", info.ID)
	if plan.Info.Spec.BeforeRun == nil {
		t.Fatal("omission removed hook")
	}
	var remove protocol.ScheduleSpec
	raw, _ := json.Marshal(spec)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["before_run"] = nil
	raw, _ = json.Marshal(fields)
	if err := json.Unmarshal(raw, &remove); err != nil {
		t.Fatal(err)
	}
	req.Spec = &remove
	if _, err := f.schedule.Manage(context.Background(), "assistant", req, 2); err == nil {
		t.Fatal("changed request null/omission accepted")
	}
	req.RequestID = "remove"
	if _, err := f.schedule.Manage(context.Background(), "assistant", req, 2); err != nil {
		t.Fatal(err)
	}
	plan, _ = f.schedule.Get(context.Background(), "assistant", info.ID)
	if plan.Info.Spec.BeforeRun != nil {
		t.Fatal("null retained hook")
	}
}

func TestProbeRestartStaleUnknownStillFencesAfterHookRemoval(t *testing.T) {
	f := setup(t)
	defer f.schedule.Close()
	var calls atomic.Int32
	f.schedule.options.Probes = probeFunc(func(_ context.Context, r ProbeRequest) (protocol.ScheduleHookResult, error) {
		calls.Add(1)
		if !r.ReconcileOnly {
			t.Error("stale recovery could launch script")
		}
		return protocol.ScheduleHookResult{}, core.ErrCleanupUnconfirmed
	})
	spec := probeSpec()
	info := create(t, f, spec, 0)
	plan, _ := f.schedule.Get(context.Background(), "assistant", info.ID)
	err := storage.Transaction(context.Background(), f.tasks.DB(), func(tx *sql.Tx) error {
		if err := f.schedule.reserveProbeTx(context.Background(), tx, "assistant", *plan, 1, "interrupted", "manual"); err != nil {
			return err
		}
		run, err := readRun(context.Background(), tx, "assistant", info.ID, "interrupted")
		if err != nil {
			return err
		}
		run.State = "hook_running"
		return writeRun(context.Background(), tx, "assistant", info.ID, *run)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.schedule.Manage(context.Background(), "assistant", Request{RequestID: "pause", Action: "pause", ScheduleID: info.ID}, 2); err != nil {
		t.Fatal(err)
	}
	awaitProbe(t, f, info.ID, func(r protocol.ScheduleRun) bool { return r.State == "hook_blocked" })
	spec.BeforeRun = nil
	spec.BeforeRunSet = true
	if _, err = f.schedule.Manage(context.Background(), "assistant", Request{RequestID: "remove", Action: "update", ScheduleID: info.ID, Spec: &spec}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err = f.schedule.Manage(context.Background(), "assistant", Request{RequestID: "bypass", Action: "run", ScheduleID: info.ID}, 4); err == nil {
		t.Fatal("removal bypassed unknown cleanup")
	}
	if err = f.schedule.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(f.schedule.options)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err = recovered.Tick(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("restarted unknown probe retried", calls.Load())
	}
}
func TestProbeMisfireAndActiveReservationPrecedeScript(t *testing.T) {
	f := setup(t)
	defer f.schedule.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	f.schedule.options.Probes = probeFunc(func(ctx context.Context, _ ProbeRequest) (protocol.ScheduleHookResult, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return protocol.ScheduleHookResult{Status: "skip"}, nil
	})
	spec := probeSpec()
	spec.Misfire = "skip"
	info := create(t, f, spec, 0)
	if err := f.schedule.Tick(context.Background(), 120000); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("misfire ran script")
	}
	if err := f.schedule.Tick(context.Background(), 180000); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := f.schedule.Tick(context.Background(), 240000); err != nil {
		t.Fatal(err)
	}
	page, _ := f.schedule.Page(context.Background(), "assistant", Query{Selected: info.ID})
	overlap, missed := false, false
	for _, r := range page.Runs {
		overlap = overlap || r.State == "skipped_overlap"
		missed = missed || r.State == "skipped_missed"
	}
	if !overlap || !missed || calls.Load() != 1 {
		t.Fatal(page, calls.Load())
	}
	close(release)
	awaitProbe(t, f, info.ID, func(r protocol.ScheduleRun) bool { return r.State == "skipped_hook" })
}
