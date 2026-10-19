package bridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func clientConfig(path string) (ClientConfig, *http.Client, error) {
	var c ClientConfig
	raw, err := os.ReadFile(path)
	if err != nil {
		return c, nil, err
	}
	if len(raw) > 16384 || json.Unmarshal(raw, &c) != nil {
		return c, nil, errors.New("invalid_mcp_pairing")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !(u.Path == "" || u.Path == "/") || !(u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) || len(c.Token) < 32 {
		return c, nil, errors.New("invalid_mcp_endpoint_or_token")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return c, nil, err
	}
	if c.CA != "" && !roots.AppendCertsFromPEM([]byte(c.CA)) {
		return c, nil, errors.New("invalid_ca")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	return c, &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_denied") }}, nil
}

// MCP proxies newline-delimited stdio JSON-RPC to the daemon's MCP endpoint.
// The daemon owns protocol negotiation, discovery, validation and authorization
// for both transports. This process never starts a model or attaches to a chat.
func MCP(in io.Reader, out io.Writer, path string) error {
	c, client, err := clientConfig(path)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxBody+1)
	version := ""
	for scanner.Scan() {
		if len(scanner.Bytes()) > maxBody {
			return errors.New("mcp_request_limit")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.Endpoint, "/")+"/mcp", bytes.NewReader(scanner.Bytes()))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if version != "" {
			req.Header.Set("MCP-Protocol-Version", version)
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			return errors.New("bridge_unreachable_check_daemon_address_tls_pairing")
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
		resp.Body.Close()
		cancel()
		if readErr != nil || len(raw) > 2*1024*1024 {
			return errors.New("bridge_response_limit")
		}
		if resp.StatusCode == http.StatusAccepted {
			continue
		}
		if resp.StatusCode != http.StatusOK || !json.Valid(raw) {
			return errors.New("bridge_mcp_transport_failed")
		}
		var envelope struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &envelope) == nil && envelope.Result.ProtocolVersion != "" {
			version = envelope.Result.ProtocolVersion
		}
		if _, err = out.Write(append(bytes.TrimSpace(raw), '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}
