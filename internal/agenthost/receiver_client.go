package agenthost

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

type receiverClient struct {
	session   *mcp.Session
	identity  agentlink.MCPReceiver
	transport *http.Transport
}

type receiverBoundedBody struct {
	io.Reader
	io.Closer
}

type receiverBearer struct {
	token    string
	endpoint *url.URL
	base     http.RoundTripper
}

func (t receiverBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	// Credentials belong exclusively to this exact configured resource. No
	// redirect, native subprocess or model tool ever receives the receiver grant.
	if r.URL.Scheme != t.endpoint.Scheme || r.URL.Host != t.endpoint.Host || r.URL.Path != t.endpoint.Path || r.URL.RawQuery != t.endpoint.RawQuery {
		return nil, agentlink.ErrUnauthorized
	}
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	resp, err := t.base.RoundTrip(req)
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

func connectReceiver(ctx context.Context, endpoint, token string, identity agentlink.MCPReceiver) (*receiverClient, error) {
	u, err := receiverURL(endpoint)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, ResponseHeaderTimeout: 35 * time.Second, IdleConnTimeout: time.Minute}
	client := &http.Client{Timeout: 40 * time.Second, Transport: receiverBearer{token, u, transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return agentlink.ErrUnauthorized }}
	session, err := mcp.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true}, mcp.Options{})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return &receiverClient{session: session, identity: identity, transport: transport}, nil
}
func (c *receiverClient) close() {
	_ = c.session.Close()
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}
func (c *receiverClient) call(ctx context.Context, name string, input, output any) error {
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
		return errors.New("receiver_remote_error")
	}
	if output == nil {
		return nil
	}
	return agentlink.DecodeMCPContract(data, output)
}
func (c *receiverClient) Register(ctx context.Context, r agentlink.MCPReceiverRegistration) error {
	var registered struct {
		ConnectionID string `json:"connection_id" required:"true"`
		NodeID       string `json:"node_id" required:"true"`
	}
	if err := c.call(ctx, "pot_receiver_register", r, &registered); err != nil {
		return err
	}
	if registered.ConnectionID != c.identity.ConnectionID() || registered.NodeID != c.identity.NodeID() {
		return agentlink.ErrUnauthorized
	}
	return nil
}
func (c *receiverClient) Claim(ctx context.Context, request string, wait int) (receiverDelivery, error) {
	var d receiverDelivery
	err := c.call(ctx, "pot_receiver_claim", struct {
		RequestID string `json:"request_id"`
		WaitMS    int    `json:"wait_ms"`
	}{request, wait}, &d)
	return d, err
}
func (c *receiverClient) Ack(ctx context.Context, a agentlink.MCPDeliveryAck) error {
	var receipt agentlink.MCPDeliveryAck
	if err := c.call(ctx, "pot_receiver_ack", a, &receipt); err != nil {
		return err
	}
	if receipt.Validate() != nil || encoded(receipt) != encoded(a) {
		return agentlink.ErrConflict
	}
	return nil
}
func (c *receiverClient) Admit(ctx context.Context, a agentlink.MCPRunAdmission) error {
	var receipt agentlink.MCPRunAdmission
	if err := c.call(ctx, "pot_receiver_admit", a, &receipt); err != nil {
		return err
	}
	if receipt.Validate() != nil || receipt != a {
		return agentlink.ErrConflict
	}
	return nil
}
func (c *receiverClient) Event(ctx context.Context, e agentlink.MCPExecutionEvent) (agentlink.MCPEventReceipt, error) {
	var receipt agentlink.MCPEventReceipt
	err := c.call(ctx, "pot_receiver_event", e, &receipt)
	return receipt, err
}
