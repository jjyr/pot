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
	return c.StreamMany(ctx, []string{path}, func(_ string, event Event) error { return receive(event) })
}
