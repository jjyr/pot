package localui

import (
	"encoding/json"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// LinkedAssistantID matches the frontend routing key, not a remote identity.
func LinkedAssistantID(link, assistant string) string { return "agent-link:" + link + ":" + assistant }

// AttentionID keeps native read receipts within their existing bounded identifier
// contract while retaining the full link and assistant identity in the hash.
// No receipt is ever rebound to a peer's short assistant ID.
func AttentionID(owner string) (string, bool) {
	if core.Identifier(owner) {
		return owner, true
	}
	if !strings.HasPrefix(owner, "agent-link:") {
		return "", false
	}
	link, assistant, ok := strings.Cut(strings.TrimPrefix(owner, "agent-link:"), ":")
	if !ok || !core.Identifier(link) || !core.Identifier(assistant) {
		return "", false
	}
	hash, err := core.HashJSON([]string{"linked-assistant-read", link, assistant})
	if err != nil {
		return "", false
	}
	return "linked-read-" + hash, true
}

type linkedAttention struct {
	ID         string `json:"id"`
	ReplyID    string `json:"reply_id"`
	ReplyCount uint64 `json:"reply_count"`
	Connected  bool   `json:"connected"`
}

func projectLinkedAttention(raw json.RawMessage) (json.RawMessage, error) {
	var page struct {
		Items []struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			Assistants []struct {
				Address    protocol.AgentRef `json:"address"`
				ReplyID    string            `json:"reply_id"`
				ReplyCount uint64            `json:"reply_count"`
			} `json:"assistants"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, err
	}
	out := []linkedAttention{}
	for _, link := range page.Items {
		if !core.Identifier(link.ID) || link.Status == "revoked" {
			continue
		}
		for _, assistant := range link.Assistants {
			if !assistant.Address.Valid() || assistant.Address.Kind != "assistant" || assistant.ReplyCount > core.MaxSafeInteger || len(assistant.ReplyID) > 256 {
				continue
			}
			out = append(out, linkedAttention{ID: LinkedAssistantID(link.ID, assistant.Address.AgentID), ReplyID: assistant.ReplyID, ReplyCount: assistant.ReplyCount, Connected: link.Status == "connected"})
		}
	}
	return json.Marshal(out)
}

// LinkedOwners maps native receipt keys back to the frontend's synthetic IDs.
func LinkedOwners(raw json.RawMessage) map[string]string {
	var rows []linkedAttention
	out := map[string]string{}
	if json.Unmarshal(raw, &rows) != nil {
		return out
	}
	for _, row := range rows {
		if key, ok := AttentionID(row.ID); ok {
			out[key] = row.ID
		}
	}
	return out
}
