package platformhelper

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
)

// GuestClient speaks the retained Python broker's authenticated HTTP protocol.
// The provider supplies its trusted address/token (and TLS transport when used).
// It never retries mutations or interprets a transport failure as successful
// cleanup. Receipt reconciliation belongs to the owning Sandbox Manager.
type GuestClient struct {
	BaseURL   string
	Token     string
	Transport http.RoundTripper
	Timeout   time.Duration
}

func (c GuestClient) Call(ctx context.Context, operation string, request any) (json.RawMessage, error) {
	method := http.MethodPost
	switch operation {
	case "health":
		method = http.MethodGet
	case "prepare", "execute", "inspect", "cancel", "finish", "capabilities", "desktop":
	default:
		return nil, errors.New("unsupported guest broker operation")
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || c.Token == "" || strings.ContainsAny(c.Token, "\r\n") {
		return nil, errors.New("invalid trusted guest endpoint or token")
	}
	var body []byte
	if method == http.MethodPost {
		body, err = json.Marshal(request)
		if err != nil {
			return nil, err
		}
		if len(body) > 3*1024*1024 {
			return nil, errors.New("guest request exceeds 3 MiB")
		}
	}
	target := strings.TrimSuffix(c.BaseURL, "/") + "/" + operation
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	client := &http.Client{Transport: c.Transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("guest broker exchange failed; operation outcome unknown: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 {
		return nil, errors.Join(errors.New("guest response truncated or exceeds 8 MiB; outcome unknown"), err)
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid guest JSON response; operation outcome unknown")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("guest broker rejected operation (HTTP %d): %.1024s", response.StatusCode, data)
	}
	return json.RawMessage(data), nil
}
