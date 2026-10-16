package eval

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigratedDeterministicScenariosExecuteProductionAgent(t *testing.T) {
	for _, name := range []string{"short-chat", "tool-round", "compaction"} {
		t.Run(name, func(t *testing.T) {
			s, err := ReadScenario(filepath.Join("../../eval/scenarios", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "run")
			report, err := Run(context.Background(), s, path, RunOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !report.Success || !report.MeasurementComplete {
				t.Fatalf("report: %+v", report)
			}
			if name == "tool-round" && (report.RequestCount != 2 || report.ToolCount != 2 || report.Usage.InputTokens == nil || *report.Usage.InputTokens != 20) {
				t.Fatalf("tool accounting: %+v", report)
			}
			if name == "compaction" && report.CompactionCount < 1 {
				t.Fatal("compaction trace did not execute actual compaction")
			}
			again, err := Validate(path)
			if err != nil || again.Success != report.Success {
				t.Fatal(again, err)
			}
			if _, err = Run(context.Background(), s, path, RunOptions{}); err == nil {
				t.Fatal("overwrote run directory")
			}
		})
	}
}
func usage(id string) UsageReport {
	return UsageReport{MeasurementID: id, SourceEventID: "event-1", Scope: "request", Source: "provider", Counter: "snapshot", Complete: true, InputTokens: value(100), OutputTokens: value(20), CacheReadTokens: value(60), ReasoningTokens: value(5)}
}
func TestUsageScopesSnapshotsDeltasAndMissingPrecision(t *testing.T) {
	a := usage("request")
	b := a
	b.SourceEventID = "event-2"
	b.InputTokens = value(120)
	native := b
	native.Source = "native"
	parent := b
	parent.Scope = "turn"
	parent.MeasurementID = "turn"
	total, err := Aggregate([]UsageReport{a, b, b, native, parent}, "request")
	if err != nil || total.Measurements != 1 || *total.InputTokens != 120 {
		t.Fatal(total, err)
	}
	partial := a
	partial.Complete = false
	total, err = Aggregate([]UsageReport{partial, usage("other")}, "request")
	if err != nil || total.InputTokens != nil {
		t.Fatal(total, err)
	}
	zero := usage("zero")
	zero.InputTokens = value(0)
	zero.OutputTokens = value(0)
	zero.CacheReadTokens = nil
	zero.ReasoningTokens = nil
	total, err = Aggregate([]UsageReport{zero}, "request")
	if err != nil || *total.InputTokens != 0 {
		t.Fatal(total, err)
	}
	a.Counter = "delta"
	b = a
	b.SourceEventID = "event-2"
	total, err = Aggregate([]UsageReport{a, a, b}, "request")
	if err != nil || *total.InputTokens != 200 || total.Measurements != 1 {
		t.Fatal(total, err)
	}
}
func TestUsageRejectsResetDisagreementEstimatesAndOverflow(t *testing.T) {
	a := usage("id")
	b := a
	b.Source = "native"
	b.InputTokens = value(101)
	if _, err := Aggregate([]UsageReport{a, b}, "request"); err == nil {
		t.Fatal("sources disagreed")
	}
	b = a
	b.Source = "estimate"
	if _, err := Aggregate([]UsageReport{b}, "request"); err == nil {
		t.Fatal("estimate accepted")
	}
	b = a
	b.CacheReadTokens = value(101)
	if _, err := Aggregate([]UsageReport{b}, "request"); err == nil {
		t.Fatal("cache subset accepted")
	}
	a.Counter = "cumulative"
	a.BaselineInput = value(80)
	a.BaselineOutput = value(10)
	total, err := Aggregate([]UsageReport{a}, "request")
	if err != nil || *total.InputTokens != 20 {
		t.Fatal(total, err)
	}
	b = a
	b.SourceEventID = "event-2"
	b.InputTokens = value(99)
	if _, err = Aggregate([]UsageReport{a, b}, "request"); err == nil {
		t.Fatal("counter reset accepted")
	}
	a = usage("a")
	a.InputTokens = value(^uint64(0))
	b = usage("b")
	if _, err = Aggregate([]UsageReport{a, b}, "request"); err == nil {
		t.Fatal("overflow accepted")
	}
}
func TestValidatorRecomputesReportsRejectsTamperingAndPathEscapes(t *testing.T) {
	s, err := ReadScenario("../../eval/scenarios/short-chat.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "run")
	report, err := Run(context.Background(), s, path, RunOptions{})
	if err != nil || !report.Success {
		t.Fatal(report, err)
	}
	WriteJSON(filepath.Join(path, "report.json"), map[string]any{"success": false})
	if reconstructed, err := Validate(path); err != nil || !reconstructed.Success {
		t.Fatal(reconstructed, err)
	}
	raw, _ := os.ReadFile(filepath.Join(path, "events.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var event Event
	core.DecodeJSON([]byte(lines[0]), &event)
	event.Seq = 99
	changed, _ := json.Marshal(event)
	lines[0] = string(changed)
	os.WriteFile(filepath.Join(path, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0600)
	if _, err = Validate(path); err == nil {
		t.Fatal("tampered ordering accepted")
	}
	s.Fixtures = map[string]string{"../../escape": "bad"}
	if _, err = Run(context.Background(), s, filepath.Join(t.TempDir(), "run"), RunOptions{}); err == nil {
		t.Fatal("fixture escaped workspace")
	}
}

func TestSmallSuiteRunsMemoryCacheDelegationAndFaults(t *testing.T) {
	report, err := RunSuite(context.Background(), filepath.Join(t.TempDir(), "suite"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report["success"] != true {
		raw, _ := json.MarshalIndent(report, "", "  ")
		t.Fatalf("suite: %s", raw)
	}
}
func TestCumulativeCounterCannotHideResetBehindMissingSnapshot(t *testing.T) {
	a := usage("id")
	a.Counter = "cumulative"
	a.BaselineInput = value(0)
	a.BaselineOutput = value(0)
	b := a
	b.SourceEventID = "event-2"
	b.InputTokens = nil
	c := a
	c.SourceEventID = "event-3"
	c.InputTokens = value(99)
	if _, err := Aggregate([]UsageReport{a, b, c}, "request"); err == nil {
		t.Fatal("reset hidden behind missing snapshot")
	}
}
