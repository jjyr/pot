package mcpreceiver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
)

type Client struct {
	session   *mcp.Session
	identity  agentlink.MCPReceiver
	transport *http.Transport
}

type receiverBoundedBody struct {
	io.Reader
	io.Closer
}

type Bearer struct {
	Token    string
	Endpoint *url.URL
	Base     http.RoundTripper
}

func (t Bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	// Credentials belong exclusively to this exact configured resource. No
	// redirect, native subprocess or model tool ever receives the receiver grant.
	if r.URL.Scheme != t.Endpoint.Scheme || r.URL.Host != t.Endpoint.Host || r.URL.Path != t.Endpoint.Path || r.URL.RawQuery != t.Endpoint.RawQuery {
		return nil, agentlink.ErrUnauthorized
	}
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", "Bearer "+t.Token)
	resp, err := t.Base.RoundTrip(req)
	if err != nil {
		return nil, errors.New("receiver_transport_unavailable")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, agentlink.MCPError{Code: agentlink.MCPUnauthorized}
	}
	resp.Body = receiverBoundedBody{Reader: io.LimitReader(resp.Body, agentlink.MCPMaxPayloadBytes+65536), Closer: resp.Body}
	return resp, nil
}

func Connect(ctx context.Context, endpoint, token string, identity agentlink.MCPReceiver) (*Client, error) {
	u, err := resourceURL(endpoint)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, ResponseHeaderTimeout: 35 * time.Second, IdleConnTimeout: time.Minute}
	client := &http.Client{Timeout: 40 * time.Second, Transport: Bearer{token, u, transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return agentlink.ErrUnauthorized }}
	session, err := mcp.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true}, mcp.Options{})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return &Client{session: session, identity: identity, transport: transport}, nil
}
func (c *Client) Close() {
	_ = c.session.Close()
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}
func (c *Client) Call(ctx context.Context, name string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	result, err := c.session.CallTool(ctx, name, raw)
	if err != nil {
		return err
	}
	if result == nil {
		return agentlink.ErrInvalid
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil || len(data) > agentlink.MCPMaxPayloadBytes {
		return agentlink.ErrInvalid
	}
	if result.IsError {
		var problem agentlink.MCPError
		if agentlink.DecodeMCPContract(data, &problem) == nil && problem.Validate() == nil {
			return problem
		}
		return agentlink.MCPError{Code: agentlink.MCPUnknown}
	}
	if output == nil {
		return nil
	}
	// Directory is the existing Agent Link catalogue with an anonymously
	// embedded Identity. Its flattened legacy JSON is validated by the owning
	// adapter; the shared strict contract decoder handles receiver DTOs below.
	if _, ok := output.(*agentlink.Directory); ok {
		return json.Unmarshal(data, output)
	}
	return agentlink.DecodeMCPContract(data, output)
}
func (c *Client) Register(ctx context.Context, r agentlink.MCPReceiverRegistration) error {
	var registered struct {
		ConnectionID string `json:"connection_id" required:"true"`
		NodeID       string `json:"node_id" required:"true"`
	}
	if err := c.Call(ctx, "pot_receiver_register", r, &registered); err != nil {
		return err
	}
	if registered.ConnectionID != c.identity.ConnectionID() || registered.NodeID != c.identity.NodeID() {
		return agentlink.ErrUnauthorized
	}
	return nil
}
func (c *Client) Claim(ctx context.Context, request string, wait int) (agentlink.MCPReceiverDelivery, error) {
	var d agentlink.MCPReceiverDelivery
	err := c.Call(ctx, "pot_receiver_claim", struct {
		RequestID string `json:"request_id"`
		WaitMS    int    `json:"wait_ms"`
	}{request, wait}, &d)
	return d, err
}
func (c *Client) Ack(ctx context.Context, a agentlink.MCPDeliveryAck) error {
	var receipt agentlink.MCPDeliveryAck
	if err := c.Call(ctx, "pot_receiver_ack", a, &receipt); err != nil {
		return err
	}
	if receipt.Validate() != nil || encode(receipt) != encode(a) {
		return agentlink.ErrConflict
	}
	return nil
}
func (c *Client) Admit(ctx context.Context, a agentlink.MCPRunAdmission) error {
	var receipt agentlink.MCPRunAdmission
	if err := c.Call(ctx, "pot_receiver_admit", a, &receipt); err != nil {
		return err
	}
	if receipt.Validate() != nil || receipt != a {
		return agentlink.ErrConflict
	}
	return nil
}
func (c *Client) Event(ctx context.Context, e agentlink.MCPExecutionEvent) (agentlink.MCPEventReceipt, error) {
	var receipt agentlink.MCPEventReceipt
	err := c.Call(ctx, "pot_receiver_event", e, &receipt)
	return receipt, err
}

func resourceURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, agentlink.ErrInvalid
	}
	return u, nil
}
func encode(value any) string { data, _ := json.Marshal(value); return string(data) }
