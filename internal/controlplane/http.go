package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/jjyr/pot/internal/core"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Server is an explicitly mounted standalone Control handler. Merely opening a
// Directory or an embedded App never binds a socket.
type Server struct {
	Directory    *Directory
	Handler      Handler
	OwnerHandler http.Handler
	OnEnroll     func(context.Context, Machine) error
	OnPresence   func(string, bool)
}

func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Directory == nil || s.Directory.options.Embedded || s.Directory.Role() != "control" {
		http.Error(w, "standalone Control required", http.StatusForbidden)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser Origin rejected", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/control/v1/identity":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		writeJSON(w, s.Directory.Identity())
	case "/control/v1/join":
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
		if err != nil {
			http.Error(w, "request exceeds limit", 413)
			return
		}
		var signed Signed
		if core.DecodeJSON(raw, &signed) != nil {
			http.Error(w, "invalid enrollment", 400)
			return
		}
		result, err := s.Directory.Join(r.Context(), signed)
		if err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		if s.OnEnroll != nil {
			var enrollment Enrollment
			if err := core.DecodeJSON(result.Payload, &enrollment); err != nil {
				http.Error(w, "invalid enrollment receipt", 500)
				return
			}
			s.Directory.mu.Lock()
			m := s.Directory.state.Members[enrollment.RuntimeID]
			s.Directory.mu.Unlock()
			if err := s.OnEnroll(r.Context(), m.Machine); err != nil {
				http.Error(w, "machine policy enrollment failed", 500)
				return
			}
		}
		writeJSON(w, result)
	case "/control/v1/connect":
		s.connect(w, r)
	default:
		if s.OwnerHandler != nil && strings.HasPrefix(r.URL.Path, "/control/v1/owner/") && s.Directory.AuthenticateOwner(bearer(r)) {
			http.StripPrefix("/control/v1/owner", s.OwnerHandler).ServeHTTP(w, r)
			return
		}
		http.Error(w, "Control authentication rejected", 403)
	}
}
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	runtimeID := r.Header.Get("Pot-Runtime-ID")
	epoch, err := strconv.ParseUint(r.Header.Get("Pot-Membership-Epoch"), 10, 64)
	if err != nil {
		http.Error(w, "invalid membership", 403)
		return
	}
	d := s.Directory
	d.mu.Lock()
	m, ok := d.state.Members[runtimeID]
	authorized := ok && m.Machine.Enabled && m.Machine.Epoch == epoch && secureToken(bearer(r), m.Token)
	d.mu.Unlock()
	if !authorized {
		http.Error(w, "invalid membership", 403)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	p := newPeer(r.Context(), c, d, m.Machine.Identity, Principal{RuntimeID: runtimeID, Epoch: epoch}, s.Handler)
	d.mu.Lock()
	current := d.state.Members[runtimeID]
	if !current.Machine.Enabled || current.Machine.Epoch != epoch || current.Token != m.Token {
		d.mu.Unlock()
		p.Close()
		return
	}
	old := d.peers[runtimeID]
	d.peers[runtimeID] = p
	d.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if s.OnPresence != nil {
		s.OnPresence(runtimeID, true)
		defer s.OnPresence(runtimeID, false)
	}
	p.run()
	d.mu.Lock()
	if d.peers[runtimeID] == p {
		delete(d.peers, runtimeID)
	}
	d.mu.Unlock()
}

type Connector struct {
	Directory  *Directory
	HTTPClient *http.Client
	Handler    Handler
	CAPEM      string
}

func (c *Connector) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	ca := c.CAPEM
	if ca == "" && c.Directory != nil {
		if m := c.Directory.Membership(); m != nil {
			ca = m.CAPEM
		}
	}
	if ca != "" {
		if client, err := ClientForCA(nil, ca); err == nil {
			return client
		}
	}
	return http.DefaultClient
}

// Preview obtains identity without trusting it. The caller must pin its ID and
// fingerprint before Enroll; custom roots belong on HTTPClient's TLS config.
func (c *Connector) Preview(ctx context.Context, origin string) (Identity, error) {
	var i Identity
	if _, err := ClientForCA(c.HTTPClient, c.CAPEM); err != nil {
		return i, err
	}
	if err := ValidOrigin(origin); err != nil {
		return i, err
	}
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(origin, "/")+"/control/v1/identity", nil)
	if err != nil {
		return i, err
	}
	if err = c.do(request, &i); err != nil {
		return i, err
	}
	return i, i.Validate()
}
func (c *Connector) do(request *http.Request, result any) error {
	// Redirects can leak invitation/member/owner credentials to another origin.
	client := *c.client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("Control HTTP status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxMessage+1))
	if err != nil {
		return err
	}
	if len(raw) > MaxMessage {
		return fmt.Errorf("Control response exceeds limit")
	}
	return core.DecodeJSON(raw, result)
}

// Enroll durably saves the original join request before networking, so uncertain
// retries redeem the same invitation/request rather than create another member.
func (c *Connector) Enroll(ctx context.Context, origin string, pinned Identity, invitation string, requestID ...string) (Membership, error) {
	if _, err := ClientForCA(c.HTTPClient, c.CAPEM); err != nil {
		return Membership{}, err
	}
	if c.Directory == nil {
		return Membership{}, fmt.Errorf("Directory required")
	}
	signed, err := c.Directory.prepareJoin(origin, pinned, invitation, requestID...)
	if err != nil {
		return Membership{}, err
	}
	data, err := json.Marshal(signed)
	if err != nil {
		return Membership{}, err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(origin, "/")+"/control/v1/join", bytes.NewReader(data))
	if err != nil {
		return Membership{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	var reply Signed
	if err = c.do(request, &reply); err != nil {
		return Membership{}, err
	}
	var enrollment Enrollment
	if err = verify(pinned, reply, &enrollment); err != nil {
		return Membership{}, err
	}
	var original JoinRequest
	if err = core.DecodeJSON(signed.Payload, &original); err != nil {
		return Membership{}, err
	}
	if enrollment.RequestID != original.RequestID || enrollment.Control != pinned {
		return Membership{}, ErrUnauthorized
	}
	membership := Membership{Origin: origin, Enrollment: enrollment, CAPEM: c.CAPEM}
	if err = c.Directory.SaveMembership(ctx, membership); err != nil {
		return Membership{}, err
	}
	return membership, nil
}

// Connect opens one outward session. It does not retry any requests. A reconnect
// is explicitly initiated by Runtime after reconciling original receipts.
func (c *Connector) Connect(ctx context.Context) (*Peer, error) {
	if c.Directory == nil {
		return nil, fmt.Errorf("Directory required")
	}
	m := c.Directory.Membership()
	if m == nil || c.Directory.Role() != "runner" {
		return nil, fmt.Errorf("enrolled Runner required")
	}
	client := *c.client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+m.Enrollment.Token)
	headers.Set("Pot-Runtime-ID", c.Directory.identity.RuntimeID)
	headers.Set("Pot-Membership-Epoch", strconv.FormatUint(m.Enrollment.Epoch, 10))
	url := strings.TrimRight(m.Origin, "/") + "/control/v1/connect"
	if strings.HasPrefix(url, "https:") {
		url = "wss:" + strings.TrimPrefix(url, "https:")
	} else {
		url = "ws:" + strings.TrimPrefix(url, "http:")
	}
	conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: &client, HTTPHeader: headers, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, err
	}
	p := newPeer(ctx, conn, c.Directory, m.Enrollment.Control, Principal{RuntimeID: m.Enrollment.Control.RuntimeID, Epoch: m.Enrollment.Epoch}, c.Handler)
	d := c.Directory
	d.mu.Lock()
	if d.state.Role != "runner" || d.state.Membership == nil || *d.state.Membership != *m {
		d.mu.Unlock()
		p.Close()
		return nil, ErrUnauthorized
	}
	old := d.peers[m.Enrollment.Control.RuntimeID]
	d.peers[m.Enrollment.Control.RuntimeID] = p
	d.mu.Unlock()
	if old != nil {
		old.Close()
	}
	go func() {
		p.run()
		d.mu.Lock()
		if d.peers[m.Enrollment.Control.RuntimeID] == p {
			delete(d.peers, m.Enrollment.Control.RuntimeID)
		}
		d.mu.Unlock()
	}()
	return p, nil
}
