package agentlink

import (
	"context"
	"reflect"
	"strings"
	"time"
)

// Observation is a live, payload-free node summary. It is never persisted in a
// connection's Directory: restarting loses the sample, not business history.
type Observation struct {
	Executor   *Activity    `json:"executor,omitempty"`
	NodeID     string       `json:"node_id"`
	Hostname   string       `json:"hostname"`
	Assistants []Activity   `json:"assistants"`
	Outbox     MessageQueue `json:"outbox"`
}

type MessageQueue struct {
	Pending    int64 `json:"pending"`
	OldestAtMS int64 `json:"oldest_at_ms"`
}

type Activity struct {
	AssistantID      string `json:"assistant_id"`
	Running          int64  `json:"running"`
	Queued           int64  `json:"queued"`
	WaitingApproval  int64  `json:"waiting_approval"`
	WaitingAllowance int64  `json:"waiting_allowance"`
	ChatState        string `json:"chat_state"`
	Fenced           bool   `json:"fenced"`
}

type ObservationSample struct {
	Value        *Observation `json:"value"`
	ObservedAtMS int64        `json:"observed_at_ms"`
	Error        string       `json:"error,omitempty"`
}

func validateObservation(value Observation, directory Directory) error {
	if a := value.Executor; a != nil {
		if !directory.Capabilities(NodeTarget(directory.NodeID)).Supports(TaskSubmit) || a.AssistantID != "" || a.Running < 0 || a.Queued < 0 || a.WaitingApproval < 0 || a.WaitingAllowance < 0 || a.Running > 1e9 || a.Queued > 1e9 || a.WaitingApproval > 1e9 || a.WaitingAllowance > 1e9 {
			return ErrConflict
		}
	}
	if value.NodeID != directory.NodeID || len(value.Hostname) > 255 || strings.ContainsAny(value.Hostname, "\r\n\x00") || len(value.Assistants) != len(directory.Assistants()) || value.Outbox.Pending < 0 || value.Outbox.Pending > 1e9 || value.Outbox.OldestAtMS < 0 {
		return ErrConflict
	}
	allowed := map[string]bool{}
	for _, a := range directory.Assistants() {
		allowed[a.Address.AgentID] = true
	}
	for _, a := range value.Assistants {
		if !allowed[a.AssistantID] || a.Running < 0 || a.Queued < 0 || a.WaitingApproval < 0 || a.WaitingAllowance < 0 || a.Running > 1e9 || a.Queued > 1e9 || a.WaitingApproval > 1e9 || a.WaitingAllowance > 1e9 {
			return ErrConflict
		}
		delete(allowed, a.AssistantID)
		switch a.ChatState {
		case "idle", "running", "queued", "waiting", "paused", "failed", "unknown":
		default:
			return ErrConflict
		}
	}
	return nil
}

func (s *Service) localObservation(ctx context.Context, peerNode string) (Observation, error) {
	d, err := s.localDirectory(ctx)
	if err != nil {
		return Observation{}, err
	}
	value, err := s.options.Backend.Observe(ctx, peerNode)
	if err == nil {
		err = validateObservation(value, d)
	}
	return value, err
}

// Observation returns a copy of the last locally received sample, without
// network I/O. An absent or failed sample is unknown, never zero work.
func (s *Service) Observation(id string) ObservationSample {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.observations[id]
	if value.Value != nil {
		copy := *value.Value
		copy.Assistants = append([]Activity(nil), copy.Assistants...)
		if copy.Executor != nil {
			executor := *copy.Executor
			copy.Executor = &executor
		}
		value.Value = &copy
	}
	return value
}

func (s *Service) sampleObservation(ctx context.Context, connection Connection, directory Directory) {
	var value Observation
	err := s.callConnection(ctx, connection, "observe", struct{}{}, &value)
	if err == nil {
		err = validateObservation(value, directory)
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	previous := s.observations[connection.ID]
	next := previous
	if err == nil {
		next = ObservationSample{Value: &value, ObservedAtMS: now}
	} else {
		next.Error = "agent_link_observation_unavailable"
	}
	changed := !reflect.DeepEqual(previous.Value, next.Value) || previous.Error != next.Error || now-s.observationNotifications[connection.ID] >= 15000
	s.observations[connection.ID] = next
	if changed {
		s.observationNotifications[connection.ID] = now
	}
	s.mu.Unlock()
	if changed && s.options.Changed != nil {
		s.options.Changed()
	}
}
