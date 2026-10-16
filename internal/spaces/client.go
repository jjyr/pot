// Package spaces composes the native App's embedded and explicitly configured
// remote Controls. Browser and CLI hosts must not construct this App lifecycle.
package spaces

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/protocol"
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
		return nil, errors.New("Space request adapter unavailable")
	}
	return f.Request(ctx, method, path, body)
}
func (f Functions) Stream(ctx context.Context, path string, receive func(Event) error) error {
	if f.Events == nil {
		return errors.New("Space event adapter unavailable")
	}
	return f.Events(ctx, path, receive)
}

type HTTPClient struct {
	origin, token, expectedID string
	client                    *http.Client
	transport                 *http.Transport
}

func NewHTTPClient(origin, token, caPEM, expectedID string) (*HTTPClient, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("Control endpoint must be an explicit HTTP(S) origin")
	}
	if token == "" || len(token) > 8192 || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("Control client credential is required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if caPEM != "" {
		if u.Scheme != "https" {
			return nil, errors.New("a Control CA requires HTTPS")
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, errors.New("invalid Control CA PEM")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &HTTPClient{strings.TrimSuffix(origin, "/"), token, expectedID, client, transport}, nil
}
func operatorPath(path string) (string, error) {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/api/v1/") || strings.Contains(u.Path, "\\") || strings.Contains(u.Path, "//") {
		return "", errors.New("invalid Space API path")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." {
			return "", errors.New("invalid Space API path")
		}
	}
	query := u.Query()
	query.Del("space_id")
	u.RawQuery = query.Encode()
	return u.RequestURI(), nil
}
func (c *HTTPClient) request(ctx context.Context, method, path string, body json.RawMessage) (*http.Response, error) {
	if method != "GET" && method != "POST" {
		return nil, errors.New("Space request method must be GET or POST")
	}
	path, err := operatorPath(path)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.origin+"/control/v1/owner"+path, bytes.NewReader(body))
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
		return nil, errors.New("Space response exceeds limit")
	}
	if !json.Valid(data) {
		return nil, errors.New("Space response is not JSON")
	}
	return data, nil
}
func (c *HTTPClient) Stream(ctx context.Context, path string, receive func(Event) error) error {
	response, err := c.request(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return errors.New("Space stream has invalid content type")
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), responseLimit)
	event := Event{}
	data := []byte{}
	flush := func() error {
		if len(data) == 0 {
			event = Event{}
			return nil
		}
		event.Data = bytes.Clone(data)
		if event.Type == "" {
			event.Type = "message"
		}
		err := receive(event)
		event = Event{}
		data = nil
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch key {
		case "event":
			event.Type = value
		case "id":
			if !strings.ContainsRune(value, 0) {
				event.ID = value
			}
		case "data":
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, value...)
			if len(data) > responseLimit {
				return errors.New("Space event exceeds limit")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return io.EOF
}
func (c *HTTPClient) Close() error { c.transport.CloseIdleConnections(); return nil }
