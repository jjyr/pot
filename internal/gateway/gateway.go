// Package gateway owns provider networking at Control. Agent and Runner receive
// an injected model port; they do not import this package or receive credentials.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrConnection = errors.New("model provider connection failed")

type Provider struct {
	ID           string   `json:"id"`
	Endpoint     string   `json:"endpoint"`
	AllowedHosts []string `json:"allowed_hosts"`
	Protocol     string   `json:"protocol"`
	SecretRef    string   `json:"secret_ref"`
}

func (p Provider) Validate() error {
	if p.ID == "" || p.SecretRef == "" {
		return errors.New("provider identity and secret reference are required")
	}
	u, err := url.Parse(p.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return errors.New("invalid provider endpoint")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("provider endpoint requires HTTP or HTTPS")
	}

	allowed := false
	for _, host := range p.AllowedHosts {
		if strings.EqualFold(host, u.Hostname()) {
			allowed = true
		}
	}
	if !allowed {
		return errors.New("provider endpoint is outside its allowed hosts")
	}
	switch p.Protocol {
	case "openai_chat", "openai_responses", "anthropic_messages", "gemini", "unknown":
	default:
		return errors.New("unsupported provider protocol")
	}
	for name, values := range u.Query() {
		if strings.Contains(strings.ToLower(name), "key") || strings.Contains(strings.ToLower(name), "token") {
			return errors.New("provider credentials must use encrypted secret references")
		}
		for _, v := range values {
			if strings.ContainsAny(v, "\r\n") {
				return errors.New("invalid provider endpoint query")
			}
		}
	}
	return nil
}

// Forward performs one POST without redirects or application retries. Its caller
// must durably reserve and claim the request before invoking it. Any transport
// failure has an uncertain outcome and must retain its reservation/receipt.
func Forward(ctx context.Context, p Provider, key []byte, body json.RawMessage, beta string, timeout time.Duration) (*http.Response, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if len(key) == 0 || bytes.ContainsAny(key, "\r\n") || strings.ContainsAny(beta, "\r\n") || timeout <= 0 {
		return nil, errors.New("invalid provider credential or request timeout")
	}
	if !json.Valid(body) {
		return nil, errors.New("invalid model JSON")
	}
	u, _ := url.Parse(p.Endpoint)
	if p.Protocol == "gemini" {
		q := u.Query()
		q.Set("alt", "sse")
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, ErrConnection
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	switch p.Protocol {
	case "anthropic_messages":
		req.Header.Set("x-api-key", string(key))
		req.Header.Set("anthropic-version", "2023-06-01")
		if beta != "" {
			req.Header.Set("anthropic-beta", beta)
		}
	case "gemini":
		req.Header.Set("x-goog-api-key", string(key))
	default:
		req.Header.Set("Authorization", "Bearer "+string(key))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = timeout
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		transport.CloseIdleConnections()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrConnection
	}
	// A response owns its body and connection until Close; the wrapper also closes
	// this request's private idle pool rather than retaining provider credentials.
	response.Body = &responseBody{ReadCloser: response.Body, closeIdle: transport.CloseIdleConnections}
	return response, nil
}

type readCloser interface {
	Read([]byte) (int, error)
	Close() error
}
type responseBody struct {
	ReadCloser readCloser
	closeIdle  func()
}

func (b *responseBody) Read(p []byte) (int, error) { return b.ReadCloser.Read(p) }
func (b *responseBody) Close() error               { err := b.ReadCloser.Close(); b.closeIdle(); return err }

// StatusError never embeds provider bodies, endpoint queries or credentials.
func StatusError(status int) error { return fmt.Errorf("model provider returned HTTP %d", status) }
