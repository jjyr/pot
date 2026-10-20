package providerallowance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
)

func snapshot(at time.Time, values ...*float64) core.AllowanceSnapshot {
	stamp := at.UTC().Format(time.RFC3339)
	windows := []core.AllowanceWindow{}
	for _, used := range values {
		windows = append(windows, core.AllowanceWindow{Used: used, IdentityQuality: "ambiguous"})
	}
	return core.AllowanceSnapshot{ReadAt: stamp, Accounts: []core.ProviderAllowance{{ReadAt: stamp, AsOf: stamp, IdentityQuality: "ambiguous", Windows: windows}}}
}
func percent(n float64) *float64 { return &n }
func evaluate(t *testing.T, s core.AllowanceSnapshot, state State, at time.Time) Transition {
	t.Helper()
	got, err := Evaluate(s, state, Options{Now: at})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestConnectionEpisodeSurvivesRestartAndTimestampDrift(t *testing.T) {
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	s := snapshot(at, percent(91), percent(95))
	s.Accounts[0].Windows[0].Pool = "shared"
	s.Accounts[0].Windows[1].Pool = "shared"
	first := evaluate(t, s, State{}, at)
	if len(first.Alerts) != 1 || first.Alerts[0].Used != 95 || first.Alerts[0].Episode != 1 || !first.State.Active {
		t.Fatal(first)
	}
	raw, _ := json.Marshal(first.State)
	var restored State
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	drift := snapshot(at, percent(97))
	drift.Accounts[0].Windows[0].ResetsAt = at.Add(5*time.Hour + time.Second).Format(time.RFC3339)
	again := evaluate(t, drift, restored, at)
	if len(again.Alerts) != 0 || again.State.Episode != 1 {
		t.Fatal(again)
	}
	low := evaluate(t, snapshot(at.Add(time.Minute), percent(30)), again.State, at.Add(time.Minute))
	if !low.State.Active || low.State.RecoveryAt == "" {
		t.Fatal(low)
	}
	duplicate := evaluate(t, snapshot(at.Add(time.Minute), percent(30)), low.State, at.Add(2*time.Minute))
	if !duplicate.State.Active {
		t.Fatal("same observation counted twice", duplicate)
	}
	recovered := evaluate(t, snapshot(at.Add(2*time.Minute), percent(30)), duplicate.State, at.Add(2*time.Minute))
	if recovered.State.Active {
		t.Fatal(recovered)
	}
	next := evaluate(t, snapshot(at.Add(3*time.Minute), percent(99)), recovered.State, at.Add(3*time.Minute))
	if len(next.Alerts) != 1 || next.Alerts[0].Episode != 2 {
		t.Fatal(next)
	}
}
func TestUnknownStaleAndEmptyDoNotRecover(t *testing.T) {
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	active := State{Version: 1, Active: true, Episode: 1, RecoveryAt: at.Add(-time.Minute).Format(time.RFC3339)}
	unknown := snapshot(at, percent(20), nil)
	stale := snapshot(at, percent(20))
	stale.Accounts[0].AsOf = at.Add(-time.Hour).Format(time.RFC3339)
	missing := snapshot(at, percent(20))
	missing.Accounts[0].ReadAt = ""
	missing.Accounts[0].AsOf = ""
	empty := core.AllowanceSnapshot{ReadAt: at.Format(time.RFC3339), Accounts: []core.ProviderAllowance{}}
	for _, s := range []core.AllowanceSnapshot{unknown, stale, missing, empty, snapshot(at.Add(-time.Hour), percent(20))} {
		got := evaluate(t, s, active, at)
		if !got.State.Active || got.State.RecoveryAt != "" || len(got.Alerts) != 0 {
			t.Fatal(got)
		}
	}
	high := evaluate(t, snapshot(at, percent(91), nil), State{}, at)
	if len(high.Alerts) != 1 {
		t.Fatal("fresh known window suppressed by unknown sibling", high)
	}
}
func TestWindowsNeverSumAndFreshSourceRequired(t *testing.T) {
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	below := evaluate(t, snapshot(at, percent(50), percent(50)), State{}, at)
	if len(below.Alerts) != 0 {
		t.Fatal(below)
	}
	for _, field := range []string{"read", "asof"} {
		s := snapshot(at, percent(100))
		if field == "read" {
			s.Accounts[0].ReadAt = ""
		} else {
			s.Accounts[0].AsOf = ""
		}
		got := evaluate(t, s, State{}, at)
		if len(got.Alerts) != 1 {
			t.Fatal(got)
		}
	}
}

func TestCachedSourcesAndUnlimitedWindowsDoNotProveRecovery(t *testing.T) {
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	active := State{Version: 1, Active: true, Episode: 1}
	first := evaluate(t, snapshot(at, percent(20)), active, at)
	cached := snapshot(at, percent(20))
	cached.ReadAt = at.Add(time.Minute).Format(time.RFC3339)
	got := evaluate(t, cached, first.State, at.Add(time.Minute))
	if !got.State.Active {
		t.Fatal("cached low counted as fresh observation", got)
	}
	unlimited := snapshot(at.Add(2*time.Minute), nil)
	unlimited.Accounts[0].Windows[0].Unlimited = true
	got = evaluate(t, unlimited, got.State, at.Add(2*time.Minute))
	if !got.State.Active || got.State.RecoveryAt != "" {
		t.Fatal(got)
	}
	mixed := snapshot(at.Add(3*time.Minute), percent(20), nil)
	mixed.Accounts[0].Windows[1].Unlimited = true
	got = evaluate(t, mixed, got.State, at.Add(3*time.Minute))
	if got.State.RecoveryAt == "" {
		t.Fatal("unlimited sibling prevented complete observation", got)
	}
	unavailable := Unavailable(got.State)
	if !unavailable.Active || unavailable.Episode != 1 || unavailable.RecoveryAt != "" || unavailable.RecoveryObservation != "" {
		t.Fatal(unavailable)
	}
}
