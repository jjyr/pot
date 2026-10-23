package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"os"
	"path/filepath"
)

func readJSON(path string, out any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) > 32*1024*1024 {
		return fmt.Errorf("record exceeds limit")
	}
	return core.DecodeJSON(raw, out)
}

type requestState struct {
	stage, turn string
	done        bool
	compaction  *string
}

// Validate reconstructs reports from immutable scenario/configuration and ordered
// records; report.json is never an authority for success or usage.
func Validate(directory string) (Report, error) {
	var manifest Manifest
	var s Scenario
	if err := readJSON(filepath.Join(directory, "manifest.json"), &manifest); err != nil {
		return Report{}, err
	}
	if err := readJSON(filepath.Join(directory, "scenario.json"), &s); err != nil {
		return Report{}, err
	}
	expected, err := resolve(s)
	if err != nil {
		return Report{}, err
	}
	hash, err := core.HashJSON(s)
	if err != nil {
		return Report{}, err
	}
	left, _ := core.HashJSON(expected)
	right, _ := core.HashJSON(manifest.EffectiveSpec)
	if manifest.SchemaVersion != Schema || manifest.RunID == "" || manifest.ScenarioID != s.ID || manifest.ScenarioHash != hash || manifest.Executor != "pot-agent-go" || manifest.DeadlineMS != s.DeadlineMS || manifest.ExecutionTarget != "fixture-host" || (manifest.Mode != "deterministic_contract" && manifest.Mode != "native_policy_live") || left != right {
		return Report{}, fmt.Errorf("manifest/scenario/effective configuration mismatch")
	}
	if err = manifest.EffectiveSpec.Validate(); err != nil {
		return Report{}, err
	}
	report := Report{SchemaVersion: Schema, RunID: manifest.RunID, ScenarioID: s.ID, ScenarioHash: hash, Mode: manifest.Mode, Errors: []string{}, StageUsage: map[string]Totals{}, TurnUsage: map[string]Totals{}, TurnAnswers: map[string]string{}}
	if err = readJSON(filepath.Join(directory, "execution.json"), &report.Errors); err != nil {
		return report, err
	}
	requests := map[string]*requestState{}
	reports := []UsageReport{}
	usageSeen := map[string]bool{}
	compactions := map[string]bool{}
	gates := map[string]*core.EventGate{}
	agentSeq := map[string]uint64{}
	terminals := map[string]bool{}
	activeTools := map[string]string{}
	messageActive := map[string]bool{}
	messageIDs := map[string]string{}
	lastText := ""
	previousTime := uint64(0)
	eventCount := uint64(0)
	turns := map[string]bool{}
	for i := range s.Turns {
		turns[fmt.Sprintf("turn-%d", i+1)] = true
	}
	file, err := os.Open(filepath.Join(directory, "events.jsonl"))
	if err != nil {
		return report, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 24*1024*1024)
	for scanner.Scan() {
		eventCount++
		if eventCount > 100000 {
			return report, fmt.Errorf("too many evaluation events")
		}
		var event Event
		if err = core.DecodeJSON(scanner.Bytes(), &event); err != nil {
			return report, err
		}
		if event.SchemaVersion != Schema || event.RunID != manifest.RunID || event.Seq != eventCount || event.ElapsedMS < previousTime || !turns[event.TurnID] {
			return report, fmt.Errorf("invalid event identity/order/time/turn")
		}
		previousTime = event.ElapsedMS
		switch event.Payload.Type {
		case "agent":
			var e core.AgentEvent
			if err = core.DecodeJSON(event.Payload.Data, &e); err != nil {
				return report, err
			}
			gate := gates[event.TurnID]
			if gate == nil {
				gate = core.NewEventGate(1, event.TurnID)
				gates[event.TurnID] = gate
			}
			if e.Seq != agentSeq[event.TurnID]+1 || !gate.Accept(e) {
				return report, fmt.Errorf("invalid Agent event order/terminal")
			}
			agentSeq[event.TurnID] = e.Seq
			key := event.TurnID + "\x00" + e.Update.ID
			switch e.Update.Type {
			case "message_started":
				if messageActive[event.TurnID] && (e.Update.ID == "" || messageIDs[event.TurnID] != e.Update.ID) {
					return report, fmt.Errorf("overlapping Agent messages")
				}
				messageActive[event.TurnID] = true
				messageIDs[event.TurnID] = e.Update.ID
			case "text_delta":
				if !messageActive[event.TurnID] {
					return report, fmt.Errorf("text outside Agent message")
				}
			case "message_finished":
				if !messageActive[event.TurnID] {
					return report, fmt.Errorf("message finish without start")
				}
				messageActive[event.TurnID] = false
				report.TurnAnswers[event.TurnID] = e.Update.Text
				lastText = e.Update.Text
			case "tool_started":
				if _, ok := activeTools[key]; ok {
					return report, fmt.Errorf("tool started twice while active")
				}
				activeTools[key] = e.Update.Name
				report.ToolCount++
			case "tool_state", "tool_output":
				if _, ok := activeTools[key]; !ok {
					return report, fmt.Errorf("output without active tool")
				}
			case "tool_finished":
				name, ok := activeTools[key]
				if !ok || name != e.Update.Name {
					return report, fmt.Errorf("tool finish without matching start")
				}
				delete(activeTools, key)
			case "turn_finished":
				terminals[event.TurnID] = true
				if e.Update.Outcome == nil {
					return report, fmt.Errorf("terminal outcome missing")
				}
				if e.Update.Outcome.Kind != "success" {
					report.Errors = append(report.Errors, "Turn outcome: "+e.Update.Outcome.Kind)
				}
			case "compaction", "compaction_observed":
			default:
				return report, fmt.Errorf("unknown Agent update %s", e.Update.Type)
			}
		case "observation":
			var o agent.Observation
			if err = core.DecodeJSON(event.Payload.Data, &o); err != nil {
				return report, err
			}
			switch o.Kind {
			case "request_started":
				if _, ok := requests[o.ID]; ok || o.ID == "" || (o.Stage != "agent" && o.Stage != "compaction") {
					return report, fmt.Errorf("invalid request start")
				}
				if o.Stage == "compaction" {
					if o.CompactionID == nil {
						return report, fmt.Errorf("compaction request has no lifecycle")
					}
					done, exists := compactions[*o.CompactionID]
					if !exists || done {
						return report, fmt.Errorf("compaction request outside lifecycle")
					}
				} else if o.CompactionID != nil {
					return report, fmt.Errorf("Agent request linked to compaction")
				}
				requests[o.ID] = &requestState{o.Stage, event.TurnID, false, o.CompactionID}
			case "usage_observed":
				request := requests[o.ID]
				if request == nil || request.done || request.turn != event.TurnID || o.Usage == nil {
					return report, fmt.Errorf("usage outside active request")
				}
				usageSeen[o.ID] = true
				reports = append(reports, observedUsage(o.ID, fmt.Sprintf("usage-%d", event.Seq), o.Usage))
			case "request_finished":
				request := requests[o.ID]
				if request == nil || request.done || request.turn != event.TurnID {
					return report, fmt.Errorf("duplicate/cross-turn request finish")
				}
				request.done = true
				if o.Error != nil {
					report.Errors = append(report.Errors, *o.Error)
				}
				if !usageSeen[o.ID] {
					reports = append(reports, observedUsage(o.ID, "finish-"+o.ID, o.Usage))
				}
			case "compaction_lifecycle":
				if o.Stage == "start" {
					if _, ok := compactions[o.ID]; ok {
						return report, fmt.Errorf("duplicate compaction start")
					}
					compactions[o.ID] = false
				} else {
					done, ok := compactions[o.ID]
					if !ok || done || (o.Stage != "complete" && o.Stage != "failed") {
						return report, fmt.Errorf("invalid compaction terminal")
					}
					compactions[o.ID] = true
				}
			case "context_estimated", "skill_diagnostic":
			default:
				return report, fmt.Errorf("unknown execution observation %s", o.Kind)
			}
		case "usage":
			var u UsageReport
			if err = core.DecodeJSON(event.Payload.Data, &u); err != nil {
				return report, err
			}
			if u.Scope != "request" && u.Scope != "turn" && u.Scope != "run" {
				return report, fmt.Errorf("unknown usage scope")
			}
			if u.Scope == "request" {
				if request := requests[u.MeasurementID]; request == nil || request.turn != event.TurnID {
					return report, fmt.Errorf("usage for unknown/cross-turn request")
				}
			}
			reports = append(reports, u)
		default:
			return report, fmt.Errorf("unknown record payload")
		}
	}
	if err = scanner.Err(); err != nil {
		return report, err
	}
	if len(activeTools) != 0 {
		report.Errors = append(report.Errors, "Unsettled tool calls")
	}
	for _, active := range messageActive {
		if active {
			report.Errors = append(report.Errors, "Unsettled Agent message")
		}
	}
	if len(terminals) != len(s.Turns) {
		report.Errors = append(report.Errors, "Not all turns completed")
	}
	for id, r := range requests {
		if !r.done {
			report.Errors = append(report.Errors, "Unsettled model request")
			if !usageSeen[id] {
				reports = append(reports, observedUsage(id, "missing-"+id, nil))
			}
		}
	}
	for _, done := range compactions {
		if !done {
			report.Errors = append(report.Errors, "Unsettled compaction")
		}
	}
	if len(s.TurnAssertions) != 0 && len(s.TurnAssertions) != len(s.Turns) {
		return report, fmt.Errorf("turn assertion count mismatch")
	}
	for i, a := range s.TurnAssertions {
		turn := fmt.Sprintf("turn-%d", i+1)
		if text, ok := report.TurnAnswers[turn]; !ok || !a.Accepts(text) {
			report.Errors = append(report.Errors, "Output assertion failed: "+turn)
		}
	}
	if s.ExpectedFinalText != nil && lastText != *s.ExpectedFinalText {
		report.Errors = append(report.Errors, "Final answer assertion failed")
	}
	root, err := os.OpenRoot(filepath.Join(directory, "workspace"))
	if err != nil {
		return report, err
	}
	for name, expected := range s.ExpectedFiles {
		data, err := root.ReadFile(name)
		if err != nil || string(data) != expected {
			report.Errors = append(report.Errors, "Artifact assertion failed: "+name)
		}
	}
	root.Close()
	for _, scope := range []string{"turn", "run"} {
		if _, err = Aggregate(reports, scope); err != nil {
			return report, err
		}
	}
	report.Usage, err = Aggregate(reports, "request")
	if err != nil {
		return report, err
	}
	for _, stage := range []string{"agent", "compaction"} {
		selected := []UsageReport{}
		for _, u := range reports {
			if r := requests[u.MeasurementID]; r != nil && r.stage == stage {
				selected = append(selected, u)
			}
		}
		report.StageUsage[stage], err = Aggregate(selected, "request")
		if err != nil {
			return report, err
		}
	}
	for turn := range turns {
		selected := []UsageReport{}
		for _, u := range reports {
			if r := requests[u.MeasurementID]; r != nil && r.turn == turn {
				selected = append(selected, u)
			}
		}
		report.TurnUsage[turn], err = Aggregate(selected, "request")
		if err != nil {
			return report, err
		}
	}
	report.RequestCount = len(requests)
	report.CompactionCount = len(compactions)
	report.MeasurementComplete = report.Usage.Measurements == len(requests) && len(requests) > 0 && report.Usage.InputTokens != nil && report.Usage.OutputTokens != nil
	for _, r := range requests {
		if !r.done {
			report.MeasurementComplete = false
		}
	}
	if len(manifest.Transport) > 0 {
		hash, err := core.HashJSON(manifest.Transport)
		if err != nil {
			return report, err
		}
		report.TransportHash = &hash
	}
	var captured []core.ModelRequest
	if err = readJSON(filepath.Join(directory, "requests.json"), &captured); err != nil {
		return report, err
	}
	seen := map[string]bool{}
	for _, r := range captured {
		if requests[r.ID] == nil || seen[r.ID] || r.Protocol != s.Protocol || r.Model != manifest.EffectiveSpec.Model || r.Provider != manifest.EffectiveSpec.Provider || !json.Valid(r.Body) {
			return report, fmt.Errorf("captured native request differs from execution identity")
		}
		seen[r.ID] = true
	}
	if manifest.Mode == "native_policy_live" {
		if err = validateNativeCapture(directory, captured, reports); err != nil {
			return report, err
		}
	}
	if len(seen) != len(requests) {
		return report, fmt.Errorf("captured request coverage differs")
	}
	report.Success = len(report.Errors) == 0
	return report, nil
}
func observedUsage(id, event string, u *modelprotocol.UsageReport) UsageReport {
	r := UsageReport{MeasurementID: id, SourceEventID: event, Scope: "request", Source: "provider", Counter: "snapshot"}
	if u != nil {
		r.InputTokens = u.InputTokens
		r.OutputTokens = u.OutputTokens
		r.CacheReadTokens = u.CacheReadTokens
		r.Complete = u.InputTokens != nil && u.OutputTokens != nil
	}
	return r
}
