package control

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

func githubFilterTools() []modelprotocol.ToolDefinition {
	list := map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}, "maxItems": 100}
	config := map[string]any{"type": "object", "properties": map[string]any{"account_id": map[string]any{"type": "string"}, "repositories": list, "reasons": list, "subject_types": list, "threads": list}, "required": []string{"account_id", "repositories", "reasons", "subject_types", "threads"}, "additionalProperties": false}
	schema, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"request_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "revision": map[string]any{"type": "integer", "minimum": 1}, "enabled": map[string]any{"type": "boolean"}, "config": config}, "required": []string{"request_id", "revision", "enabled", "config"}, "additionalProperties": false})
	return []modelprotocol.ToolDefinition{
		{Name: "github_get_notification_filters", Description: "Read the effective Pot realtime notification filters, stable connected GitHub account identity, receiving assistant and revision. Only the receiving assistant may manage its binding. This does not read GitHub notifications. Use github_read_notifications for independent paginated reads.", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "github_update_notification_filters", Description: "Replace your Pot realtime GitHub notification filter configuration for an explicit user request. Pass account_id and revision from github_get_notification_filters and a unique request_id (identical retries only). Returns the current effective configuration and revision. null means unrestricted for that field; [] means match none. Fields combine with AND, values within a list with OR. Default: reasons=[mention], other lists=null. Disable with enabled=false. Supported reasons: assign,author,comment,ci_activity,invitation,manual,mention,review_requested,security_alert,state_change,subscribed,team_mention. subject_types: Issue,PullRequest,Commit,Release,Discussion,CheckSuite,RepositoryVulnerabilityAlert. threads are numeric notification thread IDs from github_read_notifications, never PR/issue numbers. Filters only allow notifications already in the personal GitHub feed. GitHub reason is thread-level; mention can persist and does not prove each comment mentions you. author/comment do not mean direct reply. Does not change watch, thread subscription or read state. Config changes discard unaccepted pending groups, retain cursors/dedup, and fence older inflight polls. Cannot be called by external events, peers or Tasks.", InputSchema: schema},
	}
}
func (s *Service) githubFilterCall(ctx context.Context, owner string, call modelprotocol.ToolCall) (json.RawMessage, error) {
	account, err := s.githubAccount(ctx)
	if err != nil {
		return nil, err
	}
	b, err := s.githubBinding(ctx)
	if err != nil {
		return nil, err
	}
	if b.Assistant != owner {
		return nil, errors.New("github_notification_receiver_mismatch")
	}
	if call.Name == "github_update_notification_filters" {
		var command struct {
			RequestID string          `json:"request_id" required:"true"`
			Revision  int64           `json:"revision" required:"true"`
			Enabled   bool            `json:"enabled" required:"true"`
			Config    json.RawMessage `json:"config" required:"true"`
		}
		if err = core.DecodeJSON(call.Arguments, &command); err != nil {
			return nil, err
		}
		if !core.Identifier(command.RequestID) {
			return nil, errors.New("invalid_request_id")
		}
		b.Config = command.Config
		b.Revision = command.Revision
		b.Enabled = command.Enabled
		// Identity is bound by the dispatcher, never by a caller-supplied owner.
		requestID, _ := core.HashJSON([]string{owner, command.RequestID})
		if err = s.ConfigurePluginNotifications(ctx, requestID, b); err != nil {
			return nil, err
		}
		b, err = s.githubBinding(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		var empty struct{}
		if err = core.DecodeJSON(call.Arguments, &empty); err != nil {
			return nil, err
		}
	}
	return json.Marshal(map[string]any{"account_id": account, "assistant": b.Assistant, "enabled": b.Enabled, "config": b.Config, "revision": b.Revision})
}
