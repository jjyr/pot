package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/core"
)

// Fault retry policies belong only to this explicit experiment transport.
type faultServices struct {
	status, failures   int
	retry, interrupted bool
	attempts           []map[string]any
}

func (s *faultServices) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	limit := 1
	if s.retry {
		limit = 2
	}
	for attempt := 1; attempt <= limit; attempt++ {
		failed := attempt <= s.failures
		retryable := s.status == 0 || s.status == 429 || s.status == 503
		delay := 0
		if failed && s.retry && retryable && attempt < limit {
			delay = 1
		}
		s.attempts = append(s.attempts, map[string]any{"logical_request": r.ID, "attempt_id": fmt.Sprintf("%s/attempt-%d", r.ID, attempt), "attempt": attempt, "failure": failed, "http_status": s.status, "retry_delay_ms": delay, "retry_policy": "explicit evaluation transport; maximum two attempts"})
		if !failed {
			return fixtureStream(textResponse("done")), nil
		}
		if s.interrupted {
			return io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"never\",\"type\":\"function\",\"function\":{\"name\":\"exec_command\",\"arguments\":\"{\\\"cmd\\\":\\\"touch should-not-exist\\\"}\"}}]}}]")), nil
		}
		if delay == 0 {
			return nil, fmt.Errorf("evaluation transport failure status %d", s.status)
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, errors.New("evaluation attempts exhausted")
}
func (*faultServices) RPC(context.Context, *string, string, string, json.RawMessage) (json.RawMessage, *string, error) {
	return nil, nil, errors.New("external fault tools unavailable")
}
func (*faultServices) Compaction(context.Context, json.RawMessage) error { return nil }
func RunFaultSuite(ctx context.Context, path string) (map[string]any, error) {
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, err
	}
	cases := []map[string]any{}
	success := true
	for _, c := range []struct {
		name                         string
		status, failures             int
		retry, interrupted, expected bool
		attempts                     int
	}{{"control", 0, 0, false, false, true, 1}, {"retry-disabled", 429, 1, false, false, false, 1}, {"429-retry", 429, 1, true, false, true, 2}, {"503-retry", 503, 1, true, false, true, 2}, {"connection-retry", 0, 1, true, false, true, 2}, {"auth-no-retry", 401, 1, true, false, false, 1}, {"invalid-no-retry", 400, 1, true, false, false, 1}, {"retry-exhausted", 503, 2, true, false, false, 2}, {"stream-interrupted", 0, 1, true, true, false, 1}} {
		s := SmallScenario(c.name, []string{"Reply exactly done."}, true)
		text := "done"
		s.ExpectedFinalText = &text
		service := &faultServices{status: c.status, failures: c.failures, retry: c.retry, interrupted: c.interrupted}
		directory := filepath.Join(path, c.name)
		report, err := Run(ctx, s, directory, RunOptions{Services: service})
		if err != nil {
			return nil, err
		}
		if err = WriteJSON(filepath.Join(directory, "attempts.json"), service.attempts); err != nil {
			return nil, err
		}
		_, effectErr := os.Stat(filepath.Join(directory, "workspace", "should-not-exist"))
		passed := report.Success == c.expected && len(service.attempts) == c.attempts && report.ToolCount == 0 && os.IsNotExist(effectErr) && (c.expected || !report.MeasurementComplete && report.Usage.InputTokens == nil)
		success = success && passed
		cases = append(cases, map[string]any{"case": c.name, "pass": passed, "expected_success": c.expected, "actual_success": report.Success, "attempts": len(service.attempts), "usage": report.Usage, "measurement_complete": report.MeasurementComplete})
	}
	for _, duplicate := range []bool{false, true} {
		name := "tool-recovery"
		if duplicate {
			name = "duplicate-effect"
		}
		s := SmallScenario(name, []string{"Complete and reply done."}, true)
		text := "done"
		s.ExpectedFinalText = &text
		if duplicate {
			effect := fixtureCall{"effect", "exec_command", map[string]any{"cmd": "printf 'once\\n' >> effect.txt", "login": false}}
			s.Responses = []ScriptedResponse{toolResponse("", effect), toolResponse("", effect), textResponse("done")}
			s.ExpectedFiles["effect.txt"] = "once\n"
		} else {
			s.Responses = []ScriptedResponse{toolResponse("", fixtureCall{"fail", "exec_command", map[string]any{"cmd": "exit 1", "login": false}}), toolResponse("", fixtureCall{"correct", "exec_command", map[string]any{"cmd": "true", "login": false}}), textResponse("done")}
		}
		report, err := Run(ctx, s, filepath.Join(path, name), RunOptions{})
		if err != nil {
			return nil, err
		}
		passed := report.Success && report.ToolCount == 2
		success = success && passed
		cases = append(cases, map[string]any{"case": name, "pass": passed, "actual_success": report.Success, "usage": report.Usage, "attempts": report.RequestCount})
	}
	summary := map[string]any{"mode": "deterministic_contract", "success": success, "cases": cases, "empirical_efficiency_claim": false, "retry_scope": "explicit evaluation transport; production automatic retry stays disabled"}
	if err := WriteJSON(filepath.Join(path, "summary.json"), summary); err != nil {
		return nil, err
	}
	return summary, nil
}
