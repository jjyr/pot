package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type AskRequest struct {
	Message      protocol.SendMessage `json:"message"`
	Model        string               `json:"model,omitempty"`
	Effort       string               `json:"effort,omitempty"`
	Provider     string               `json:"provider,omitempty"`
	TaskModel    string               `json:"task_model,omitempty"`
	TaskEffort   string               `json:"task_effort,omitempty"`
	TaskProvider string               `json:"task_provider,omitempty"`
}

// Ask freezes experiment configuration before accepting input. Exact retries
// reuse that snapshot, even if the editable assistant configuration has changed.
func (s *Service) Ask(ctx context.Context, owner string, r AskRequest) (protocol.SendMessageAccepted, error) {
	if !core.Identifier(owner) {
		return protocol.SendMessageAccepted{}, errors.New("invalid assistant")
	}
	if err := r.Message.Validate(); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	hash, err := core.HashJSON(r)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	var old string
	err = s.tasksDB.QueryRowContext(ctx, "SELECT input_hash FROM experiment_requests WHERE assistant_id=? AND request_id=?", owner, r.Message.RequestID).Scan(&old)
	if err == nil {
		if old != hash {
			return protocol.SendMessageAccepted{}, errors.New("ask request identity conflict")
		}
		return c.Send(ctx, r.Message)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return protocol.SendMessageAccepted{}, err
	}
	state, err := c.NextConfiguration(ctx)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if state.Config == nil {
		return protocol.SendMessageAccepted{}, errors.New("assistant model is not configured")
	}
	main, err := s.experimentModel(ctx, *state.Config, r.Provider, r.Model, r.Effort)
	if err != nil {
		return protocol.SendMessageAccepted{}, fmt.Errorf("assistant: %w", err)
	}
	var taskRaw any
	if r.TaskModel != "" || r.TaskEffort != "" || r.TaskProvider != "" {
		base := *state.Config
		if len(state.Intent) > 0 && string(state.Intent) != "null" {
			var selection AssistantSelection
			if err = json.Unmarshal(state.Intent, &selection); err != nil {
				return protocol.SendMessageAccepted{}, err
			}
			if selection.TaskDefault != nil {
				base, err = s.resolveTaskDefault(ctx, *selection.TaskDefault)
				if err != nil {
					return protocol.SendMessageAccepted{}, err
				}
			}
		}
		task, e := s.experimentModel(ctx, base, r.TaskProvider, r.TaskModel, r.TaskEffort)
		if e != nil {
			return protocol.SendMessageAccepted{}, fmt.Errorf("task: %w", e)
		}
		raw, e := json.Marshal(task)
		if e != nil {
			return protocol.SendMessageAccepted{}, e
		}
		taskRaw = string(raw)
	}
	raw, err := json.Marshal(main)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	_, err = s.tasksDB.ExecContext(ctx, "INSERT INTO experiment_requests(assistant_id,request_id,input_hash,main_spec,task_spec) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING", owner, r.Message.RequestID, hash, string(raw), taskRaw)
	if err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if err = s.tasksDB.QueryRowContext(ctx, "SELECT input_hash FROM experiment_requests WHERE assistant_id=? AND request_id=?", owner, r.Message.RequestID).Scan(&old); err != nil {
		return protocol.SendMessageAccepted{}, err
	}
	if old != hash {
		return protocol.SendMessageAccepted{}, errors.New("ask request identity conflict")
	}
	return c.Send(ctx, r.Message)
}

func (s *Service) experimentModel(ctx context.Context, base core.ResolvedSpec, provider, model, effort string) (core.ResolvedSpec, error) {
	for _, v := range []string{provider, model, effort} {
		if len(v) > 256 || strings.ContainsFunc(v, unicode.IsControl) || v != "" && strings.TrimSpace(v) != v {
			return base, errors.New("invalid model override")
		}
	}
	if provider != "" && !core.Identifier(provider) {
		return base, errors.New("invalid Provider identity")
	}
	if effort != "" && !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "default"}, effort) {
		return base, errors.New("unsupported reasoning effort")
	}
	if provider != "" || model != "" {
		id := base.SourceProfileID
		if provider != "" {
			id = provider
		}
		var chosen *core.Profile
		for _, p := range s.Profiles.savedSnapshot().Profiles {
			if p.ID == id {
				copy := p
				chosen = &copy
				break
			}
		}
		if chosen == nil {
			return base, errors.New("Provider unavailable")
		}
		name := model
		if name == "" {
			name = base.Model
			if id != base.SourceProfileID {
				name = chosen.Model
			}
		}
		selection := AssistantSelection{ProfileID: id, ProfileRevision: chosen.Revision, Model: name, ReasoningEffort: base.Params.ReasoningEffort}
		if name == base.Model && id == base.SourceProfileID {
			window, output := base.Params.Agent.ContextWindow, base.Params.MaxOutputTokens
			selection.ContextWindow = &window
			selection.MaxOutputTokens = &output
		}
		resolved, err := s.Profiles.ResolveAssistant(selection)
		if err != nil {
			return base, err
		}
		base = resolved
	}
	if effort != "" {
		if !slices.Contains([]string{"openai_chat", "openai_responses", "anthropic_messages", "gemini"}, base.GatewayProtocol) {
			return base, errors.New("Provider protocol cannot encode an explicit reasoning effort")
		}
		if effort == "default" {
			base.Params.ReasoningEffort = nil
		} else {
			base.Params.ReasoningEffort = &effort
		}
	}
	if err := s.Profiles.sourceAvailable(ctx, base.Provider); err != nil {
		return base, err
	}
	return base, base.Rehash()
}

func (s *Service) taskExperimentRoot(ctx context.Context, owner, task string) (string, error) {
	rows, err := s.tasksDB.QueryContext(ctx, `WITH RECURSIVE lineage(id) AS (SELECT ? UNION SELECT r.source_task FROM task_retries r JOIN lineage l ON r.task_id=l.id WHERE r.assistant_id=?) SELECT DISTINCT e.experiment_id FROM experiment_task_requests e JOIN task_writes w ON w.assistant_id=e.assistant_id AND w.request_id=e.request_id WHERE w.assistant_id=? AND w.task_id IN (SELECT id FROM lineage)`, task, owner, owner)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	root := ""
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return "", err
		}
		if root != "" && root != v {
			return "", errors.New("conflicting task experiment identities")
		}
		root = v
	}
	return root, rows.Err()
}

func (s *Service) experimentRoot(ctx context.Context, owner, turn string) (string, error) {
	var root string
	err := s.tasksDB.QueryRowContext(ctx, "SELECT request_id FROM experiment_requests WHERE assistant_id=? AND request_id=?", owner, turn).Scan(&root)
	if err == nil {
		return root, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if !strings.HasPrefix(turn, "pot-result-") {
		return "", nil
	}
	// Load delivery membership first; never hold a database cursor while resolving
	// task ownership against the same single-connection database.
	rows, err := s.tasksDB.QueryContext(ctx, `SELECT envelope FROM agent_messages WHERE recipient=? AND id IN (SELECT message FROM agent_delivery_items WHERE batch=?) ORDER BY sequence`, owner, strings.TrimPrefix(turn, "pot-result-"))
	if err != nil {
		return "", err
	}
	var mail []protocol.Envelope
	for rows.Next() {
		var raw string
		var m protocol.Envelope
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return "", err
		}
		if err = json.Unmarshal([]byte(raw), &m); err != nil {
			rows.Close()
			return "", err
		}
		mail = append(mail, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	for i, m := range mail {
		task := ""
		if m.Result != nil {
			task = m.Result.TaskID
		} else if m.From.Kind == "task" && m.From.NodeID == s.options.RuntimeID {
			task = m.From.AgentID
		}
		key := ""
		if task != "" {
			key, err = s.taskExperimentRoot(ctx, owner, task)
			if err != nil {
				return "", err
			}
		}
		if i > 0 && key != root {
			return "", errors.New("result batch mixes experiment configurations")
		}
		root = key
	}
	return root, nil
}

func (s *Service) experimentTurnConfig(ctx context.Context, owner, turn string, base core.ResolvedSpec) (core.ResolvedSpec, error) {
	root, err := s.experimentRoot(ctx, owner, turn)
	if err != nil || root == "" {
		return base, err
	}
	var raw string
	if err = s.tasksDB.QueryRowContext(ctx, "SELECT main_spec FROM experiment_requests WHERE assistant_id=? AND request_id=?", owner, root).Scan(&raw); err != nil {
		return base, err
	}
	var spec core.ResolvedSpec
	if err = json.Unmarshal([]byte(raw), &spec); err != nil {
		return base, err
	}
	return spec, spec.Validate()
}

func (s *Service) bindExperimentTask(ctx context.Context, owner, turn, request string) error {
	root, err := s.experimentRoot(ctx, owner, turn)
	if err != nil || root == "" {
		return err
	}
	_, err = s.tasksDB.ExecContext(ctx, "INSERT INTO experiment_task_requests(assistant_id,request_id,experiment_id) VALUES(?,?,?) ON CONFLICT DO NOTHING", owner, request, root)
	return err
}

func (s *Service) experimentTaskConfig(ctx context.Context, owner, request string) (*core.ResolvedSpec, error) {
	var raw sql.NullString
	err := s.tasksDB.QueryRowContext(ctx, `SELECT r.task_spec FROM experiment_requests r JOIN experiment_task_requests t ON r.assistant_id=t.assistant_id AND r.request_id=t.experiment_id WHERE t.assistant_id=? AND t.request_id=?`, owner, request).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !raw.Valid {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var spec core.ResolvedSpec
	if err = json.Unmarshal([]byte(raw.String), &spec); err != nil {
		return nil, err
	}
	return &spec, spec.Validate()
}
