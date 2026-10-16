package sandboxprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/internal/sandbox"
)

type desktopEndpoint struct {
	url, token string
	transport  http.RoundTripper
}

func (e desktopEndpoint) request(ctx context.Context, method, path string, value any) (json.RawMessage, error) {
	var body []byte
	if value != nil {
		body, _ = json.Marshal(value)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.url+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Pot-Token", e.token)
	if value != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Transport: e.transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, errors.New("private desktop exchange unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, errors.New("private desktop response invalid")
	}
	if response.StatusCode != http.StatusOK || !json.Valid(data) {
		return nil, errors.New("private desktop service unavailable")
	}
	return data, nil
}
func (e desktopEndpoint) prepare(ctx context.Context) error {
	deadline := time.NewTimer(25 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := e.request(ctx, http.MethodGet, "/api/status", nil); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("desktop service did not become ready")
		case <-time.After(200 * time.Millisecond):
		}
	}
}
func (e desktopEndpoint) open(ctx context.Context) (io.ReadWriteCloser, error) {
	if _, err := e.request(ctx, http.MethodPost, "/api/control", map[string]any{"action": "take"}); err != nil {
		return nil, err
	}
	u, err := url.Parse(e.url + "/rfb")
	if err != nil {
		return nil, err
	}
	u.Scheme = "ws"
	client := &http.Client{Transport: e.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"X-Pot-Token": {e.token}}, Subprotocols: []string{"binary"}})
	if err != nil {
		return nil, errors.New("private desktop stream unavailable")
	}
	conn.SetReadLimit(16 * 1024 * 1024)
	return websocket.NetConn(ctx, conn, websocket.MessageBinary), nil
}
func desktopCredential(raw json.RawMessage) (string, error) {
	var value struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &value) != nil || len(value.Token) < 16 || len(value.Token) > 256 {
		return "", errors.New("private desktop credential unavailable")
	}
	return value.Token, nil
}
func linuxDesktopCapabilities() sandbox.DesktopCapabilities {
	return sandbox.DesktopCapabilities{Viewing: true, Input: true, Clipboard: true, Browser: true, Protocol: "rfb-3.8"}
}
func (p *AppleContainer) DesktopCapabilities() sandbox.DesktopCapabilities {
	return linuxDesktopCapabilities()
}
func (p *AppleContainer) desktopEndpoint(ctx context.Context, i sandbox.Instance) (desktopEndpoint, error) {
	raw, err := p.Request(ctx, i, "desktop", json.RawMessage(`{}`), 30*time.Second)
	if err != nil {
		return desktopEndpoint{}, err
	}
	token, err := desktopCredential(raw)
	if err != nil {
		return desktopEndpoint{}, err
	}
	state, err := p.state(ctx, i, "status")
	if err != nil {
		return desktopEndpoint{}, err
	}
	address, _ := state["ip"].(string)
	ip := net.ParseIP(address)
	if ip == nil {
		return desktopEndpoint{}, errors.New("desktop address unavailable")
	}
	return desktopEndpoint{url: "http://" + net.JoinHostPort(ip.String(), "17681"), token: token, transport: &http.Transport{Proxy: nil}}, nil
}
func (p *AppleContainer) PrepareDesktop(ctx context.Context, i sandbox.Instance) error {
	e, err := p.desktopEndpoint(ctx, i)
	if err != nil {
		return err
	}
	return e.prepare(ctx)
}
func (p *AppleContainer) OpenDesktop(ctx context.Context, i sandbox.Instance) (io.ReadWriteCloser, error) {
	e, err := p.desktopEndpoint(ctx, i)
	if err != nil {
		return nil, err
	}
	return e.open(ctx)
}
func (p *AppleContainer) DesktopMetrics(ctx context.Context, i sandbox.Instance) (json.RawMessage, error) {
	e, err := p.desktopEndpoint(ctx, i)
	if err != nil {
		return nil, err
	}
	raw, err := e.request(ctx, http.MethodGet, "/api/metrics", nil)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("desktop metrics object required")
	}
	var prepared sandbox.PreparedResources
	if err = loadJSON(filepath.Join(i.Directory(), "prepared-resources.json"), &prepared); err != nil {
		return nil, err
	}
	fields["image"], _ = json.Marshal(prepared.ImageReference)
	return json.Marshal(fields)
}
func (p *AppleContainer) DesktopBrowser(ctx context.Context, i sandbox.Instance) error {
	e, err := p.desktopEndpoint(ctx, i)
	if err != nil {
		return err
	}
	_, err = e.request(ctx, http.MethodPost, "/api/browser", map[string]any{})
	return err
}

func (p *Podman) DesktopCapabilities() sandbox.DesktopCapabilities { return linuxDesktopCapabilities() }
func (p *Podman) desktopEndpoint(ctx context.Context, i sandbox.Instance) (desktopEndpoint, error) {
	raw, err := p.Request(ctx, i, "desktop", json.RawMessage(`{}`), 30*time.Second)
	if err != nil {
		return desktopEndpoint{}, err
	}
	token, err := desktopCredential(raw)
	if err != nil {
		return desktopEndpoint{}, err
	}
	saved, err := p.load(i)
	if err != nil || saved == nil {
		return desktopEndpoint{}, errors.New("desktop Sandbox unavailable")
	}
	row, err := p.inspect(ctx, *saved)
	if err != nil {
		return desktopEndpoint{}, err
	}
	ports, ok := nested(row, "NetworkSettings", "Ports", "17681/tcp").([]any)
	if !ok || len(ports) != 1 {
		return desktopEndpoint{}, errors.New("desktop port unavailable")
	}
	port, ok := ports[0].(map[string]any)
	if !ok || port["HostIp"] != "127.0.0.1" {
		return desktopEndpoint{}, errors.New("desktop must use private loopback")
	}
	text, _ := port["HostPort"].(string)
	number, err := strconv.ParseUint(text, 10, 16)
	if err != nil || number == 0 {
		return desktopEndpoint{}, errors.New("desktop port invalid")
	}
	return desktopEndpoint{url: "http://127.0.0.1:" + text, token: token, transport: &http.Transport{Proxy: nil}}, nil
}
func (p *Podman) PrepareDesktop(ctx context.Context, i sandbox.Instance) error {
	e, err := p.desktopEndpoint(ctx, i)
	if err != nil {
		return err
	}
	return e.prepare(ctx)
}
func (p *Podman) OpenDesktop(ctx context.Context, i sandbox.Instance) (io.ReadWriteCloser, error) {
	e, err := p.desktopEndpoint(ctx, i)
	if err != nil {
		return nil, err
	}
	return e.open(ctx)
}
func (p *Podman) DesktopMetrics(ctx context.Context, i sandbox.Instance) (json.RawMessage, error) {
	e, err := p.desktopEndpoint(ctx, i)
	if err != nil {
		return nil, err
	}
	return e.request(ctx, http.MethodGet, "/api/metrics", nil)
}
func (p *Podman) DesktopBrowser(ctx context.Context, i sandbox.Instance) error {
	e, err := p.desktopEndpoint(ctx, i)
	if err != nil {
		return err
	}
	_, err = e.request(ctx, http.MethodPost, "/api/browser", map[string]any{})
	return err
}

func (p *AppleVM) DesktopCapabilities() sandbox.DesktopCapabilities {
	return sandbox.DesktopCapabilities{Viewing: true, Input: true, Protocol: "rfb-3.8"}
}
func (p *AppleVM) PrepareDesktop(ctx context.Context, i sandbox.Instance) error {
	client, err := p.guest(ctx, i)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+"/desktop-ready", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+client.Token)
	response, err := (&http.Client{Transport: client.Transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return errors.New("enrolled desktop unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("configure enrolled guest loopback RFB 3.8 desktop")
	}
	return nil
}
func (p *AppleVM) OpenDesktop(ctx context.Context, i sandbox.Instance) (io.ReadWriteCloser, error) {
	client, err := p.guest(ctx, i)
	if err != nil {
		return nil, err
	}
	transport := client.Transport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+"/desktop-stream", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+client.Token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "pot-rfb")
	response, err := (&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return nil, errors.New("private enrolled desktop stream unavailable")
	}
	if response.StatusCode != 101 {
		response.Body.Close()
		return nil, errors.New("enrolled desktop did not upgrade")
	}
	stream, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		response.Body.Close()
		return nil, errors.New("enrolled desktop transport is not duplex")
	}
	return stream, nil
}
func (p *AppleVM) DesktopMetrics(context.Context, sandbox.Instance) (json.RawMessage, error) {
	return nil, errors.New("enrolled guest desktop metrics unavailable")
}
func (p *AppleVM) DesktopBrowser(context.Context, sandbox.Instance) error {
	return errors.New("enrolled guest desktop browser unavailable")
}
