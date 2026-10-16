package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/browserpush"
	"github.com/jjyr/pot/internal/protocol"
	"io"
	"net/http"
)

// NotificationSource reaches only the configured Control, with its server-held
// credential and pinned identity. Offline failure never reads another Runtime.
type NotificationSource struct {
	ID       string
	Upstream Upstream
}

func (s NotificationSource) Identity() string { return s.ID }
func (s NotificationSource) Feed(ctx context.Context, after map[string]int64, initialize bool) (browserpush.Feed, error) {
	result := browserpush.Feed{}
	payload, _ := json.Marshal(map[string]any{"after": after, "initialize": initialize})
	r, err := http.NewRequestWithContext(ctx, "POST", s.Upstream.URL+"/api/v1/assistant-notifications", bytes.NewReader(payload))
	if err != nil {
		return result, err
	}
	r.Header.Set("Authorization", "Bearer "+s.Upstream.Credential)
	r.Header.Set("X-Pot-Control-ID", s.ID)
	r.Header.Set("Content-Type", "application/json")
	response, err := s.Upstream.Client.Do(r)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return result, errors.New("configured Control notification feed unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024+1))
	if err != nil || len(data) > 16*1024*1024 {
		return result, errors.New("notification feed exceeds limit")
	}
	var feed protocol.AssistantNotifications
	if err = json.Unmarshal(data, &feed); err != nil {
		return result, err
	}
	result.Cursors = feed.Cursors
	for _, n := range feed.Messages {
		if err = n.Message.Validate(); err != nil {
			return result, err
		}
		if n.Sequence < 1 {
			return result, errors.New("invalid notification sequence")
		}
		result.Messages = append(result.Messages, browserpush.Notification{AssistantID: n.AssistantID, Sequence: n.Sequence, MessageID: n.Message.ID, Text: n.Message.Text, CreatedAtMS: n.Message.CreatedAtMS, Final: n.Message.Role == protocol.Assistant && n.Message.Status == protocol.Complete})
	}
	return result, nil
}

func (s *Server) AttachPush(root string, source browserpush.Source, sender browserpush.Sender) error {
	push, err := browserpush.Open(browserpush.Options{Root: root, Source: source, Sender: sender, SecureCookies: s.origin.Scheme == "https", CredentialEpoch: s.CredentialEpoch, CheckOrigin: s.CheckOrigin, Authorize: func(r *http.Request) (browserpush.Identity, error) {
		owner, valid, err := s.Authorize(r)
		return browserpush.Identity{Owner: owner, Valid: valid}, err
	}})
	if err != nil {
		return err
	}
	s.push = push
	return nil
}
func (s *Server) pushKey() string {
	if s.push == nil {
		return ""
	}
	return s.push.PublicKey()
}
func (s *Server) RunNotifications(ctx context.Context) error {
	if s.push == nil {
		return nil
	}
	return s.push.Run(ctx)
}
