package localui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"net/http"
	"time"
)

func (c *HTTPClient) StreamMany(ctx context.Context, paths []string, receive func(string, Event) error) error {
	if c.client == nil || c.token == "" {
		return errors.New("native local UI transport unavailable")
	}
	if len(paths) == 0 {
		return errors.New("local UI stream requires at least one path")
	}
	if len(paths) > 8 {
		return errors.New("local UI stream supports at most 8 paths")
	}
	validated := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		vp, err := operatorPath(p)
		if err != nil {
			return err
		}
		if _, ok := seen[vp]; ok {
			return errors.New("local UI stream received duplicate path")
		}
		seen[vp] = struct{}{}
		validated = append(validated, vp)
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.token)
	if c.expectedID != "" {
		header.Set("X-Pot-Control-ID", c.expectedID)
	}
	conn, response, err := websocket.Dial(ctx, c.origin+"/api/v1/ws", &websocket.DialOptions{
		HTTPClient:   c.client,
		HTTPHeader:   header,
		Subprotocols: []string{"pot.v1"},
	})
	if err != nil {
		if response != nil {
			if response.Body != nil {
				response.Body.Close()
			}
		}
		return err
	}
	defer conn.CloseNow()
	if conn.Subprotocol() != "pot.v1" {
		return errors.New("local UI stream did not negotiate pot.v1")
	}
	conn.SetReadLimit(responseLimit)

	ids := make([]string, len(validated))
	for i := range validated {
		ids[i] = fmt.Sprintf("s%d", i)
	}

	subs := make([]struct {
		ID                string `json:"id"`
		Type              string `json:"type"`
		Path              string `json:"path"`
		ExpectedControlID string `json:"expected_control_id,omitempty"`
	}, len(validated))
	for i, p := range validated {
		subs[i].ID = ids[i]
		subs[i].Type = "subscribe"
		subs[i].Path = p
		subs[i].ExpectedControlID = c.expectedID
	}

	acked := make([]bool, len(ids))
	submitted := make([]bool, len(ids))
	idIndex := make(map[string]int, len(ids))
	for i, id := range ids {
		idIndex[id] = i
	}

	writeSubscription := func(payload []byte) error {
		writeCtx, writeCancel := context.WithTimeout(ctx, 15*time.Second)
		defer writeCancel()
		return conn.Write(writeCtx, websocket.MessageText, payload)
	}

	defer func() {
		unsubCtx, unsubCancel := context.WithTimeout(context.Background(), time.Second)
		defer unsubCancel()
		for i := range subs {
			if !submitted[i] {
				continue
			}
			payload, err := json.Marshal(struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}{ID: ids[i], Type: "unsubscribe"})
			if err != nil {
				continue
			}
			_ = conn.Write(unsubCtx, websocket.MessageText, payload)
		}
	}()

	for i := range subs {
		payload, err := json.Marshal(subs[i])
		if err != nil {
			return err
		}
		if err := writeSubscription(payload); err != nil {
			return err
		}
		submitted[i] = true
	}

	type frame struct {
		Type    string             `json:"type"`
		ID      string             `json:"id"`
		Event   string             `json:"event"`
		EventID string             `json:"event_id"`
		Data    json.RawMessage    `json:"data"`
		Status  int                `json:"status"`
		Error   *protocol.APIError `json:"error"`
	}

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return io.EOF
			}
			return err
		}
		if typ != websocket.MessageText {
			return errors.New("local UI stream received non-text frame")
		}
		var f frame
		if err := json.Unmarshal(data, &f); err != nil {
			return err
		}
		if f.Type == "heartbeat" {
			continue
		}
		idx, ok := idIndex[f.ID]
		if !ok {
			return errors.New("local UI stream received unexpected frame id")
		}
		if !acked[idx] {
			if f.Type == "event" {
				return errors.New("local UI stream received event before acknowledgement")
			}
			if f.Type != "response" {
				return errors.New("local UI stream received unexpected frame")
			}
			if f.Status != http.StatusOK || f.Error != nil {
				if f.Error != nil {
					return *f.Error
				}
				return errors.New("Control rejected stream subscription")
			}
			acked[idx] = true
			continue
		}
		switch f.Type {
		case "event":
			if err := receive(validated[idx], Event{Type: f.Event, ID: f.EventID, Data: f.Data}); err != nil {
				return err
			}
		case "end":
			if f.Error != nil {
				return *f.Error
			}
			return io.EOF
		default:
			return errors.New("local UI stream received unexpected frame")
		}
	}
}
