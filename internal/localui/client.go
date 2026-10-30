package localui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/pkg/protocol"
)

const responseLimit = 16 << 20

type Event struct {
	Type string
	ID   string
	Data json.RawMessage
}
type Caller interface {
	Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
	Stream(context.Context, string, func(Event) error) error
}

// Functions adapts in-process services without giving either WebView credentials.
type Functions struct {
	Request func(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
	Events  func(context.Context, string, func(Event) error) error
}

func (f Functions) Call(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
	if f.Request == nil {
		return nil, errors.New("local UI request adapter unavailable")
	}
	return f.Request(ctx, method, path, body)
}
func (f Functions) Stream(ctx context.Context, path string, receive func(Event) error) error {
	if f.Events == nil {
		return errors.New("local UI event adapter unavailable")
	}
	return f.Events(ctx, path, receive)
}

type HTTPClient struct {
	origin, token, expectedID string
	client                    *http.Client
}

// NewLocalHTTPClient uses an injected private Unix-socket HTTP transport. The
// token stays in the native host, and only streams disable the Client timeout;
// ordinary calls retain their own bounded request context.
func NewLocalHTTPClient(client *http.Client, credential, expectedID string) *HTTPClient {
	var local *http.Client
	if client != nil {
		copy := *client
		copy.Timeout = 0
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		local = &copy
	}
	return &HTTPClient{origin: "http://runtime", token: credential, expectedID: expectedID, client: local}
}

// NewHTTPClient keeps credentials in the host and supports the same private
// Control resource/stream contract used by the native UI.
func NewHTTPClient(origin string, client *http.Client, credential, expectedID string) *HTTPClient {
	c := NewLocalHTTPClient(client, credential, expectedID)
	c.origin = strings.TrimSuffix(origin, "/")
	return c
}

func operatorPath(path string) (string, error) {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/api/v1/") || strings.Contains(u.Path, "\\") || strings.Contains(u.Path, "//") {
		return "", errors.New("invalid local UI API path")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." {
			return "", errors.New("invalid local UI API path")
		}
	}
	query := u.Query()
	query.Del("space_id")
	u.RawQuery = query.Encode()
	return u.RequestURI(), nil
}
func (c *HTTPClient) request(ctx context.Context, method, path string, body json.RawMessage) (*http.Response, error) {
	if c.client == nil || c.token == "" {
		return nil, errors.New("native local UI transport unavailable")
	}
	path, err := operatorPath(path)
	if err != nil {
		return nil, err
	}
	if method != "GET" && method != "POST" {
		if method != "DELETE" {
			return nil, errors.New("local UI request method must be GET, POST, or DELETE")
		}
		parsed, perr := url.ParseRequestURI(path)
		if perr != nil || !strings.HasPrefix(parsed.Path, "/api/v1/integrations/") {
			return nil, errors.New("local UI request method must be GET or POST")
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if c.expectedID != "" {
		request.Header.Set("X-Pot-Control-ID", c.expectedID)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<16))
		var failure protocol.APIError
		if json.Unmarshal(data, &failure) == nil && failure.Message != "" {
			return nil, failure
		}
		return nil, fmt.Errorf("Control rejected request (HTTP %d)", response.StatusCode)
	}
	return response, nil
}
func (c *HTTPClient) Call(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := c.request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > responseLimit {
		return nil, errors.New("local UI response exceeds limit")
	}
	if !json.Valid(data) {
		return nil, errors.New("local UI response is not JSON")
	}
	return data, nil
}

func (c *HTTPClient) Stream(ctx context.Context, path string, receive func(Event) error) error {
	if c.client == nil || c.token == "" {
		return errors.New("native local UI transport unavailable")
	}
	validatedPath, err := operatorPath(path)
	if err != nil {
		return err
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

	subscribe := struct {
		ID                string `json:"id"`
		Type              string `json:"type"`
		Path              string `json:"path"`
		ExpectedControlID string `json:"expected_control_id,omitempty"`
	}{
		ID:                "stream",
		Type:              "subscribe",
		Path:              validatedPath,
		ExpectedControlID: c.expectedID,
	}
	payload, err := json.Marshal(subscribe)
	if err != nil {
		return err
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, 15*time.Second)
	err = conn.Write(writeCtx, websocket.MessageText, payload)
	writeCancel()
	if err != nil {
		return err
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

	acked := false
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
		if f.ID != "stream" {
			return errors.New("local UI stream received unexpected frame id")
		}
		if !acked {
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
			acked = true
			continue
		}
		switch f.Type {
		case "event":
			if err := receive(Event{Type: f.Event, ID: f.EventID, Data: f.Data}); err != nil {
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
