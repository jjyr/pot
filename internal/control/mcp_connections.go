package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// PotMCPConnection exposes public state only. Tokens, PKCE and SDK sessions are
// never directory identities and never leave the private Control/secrets store.
type PotMCPConnection struct {
	Inbound      bool                 `json:"inbound,omitempty"`
	ID           string               `json:"id"`
	URL          string               `json:"url"`
	NodeID       string               `json:"node_id"`
	RemoteNodeID string               `json:"remote_node_id"`
	State        string               `json:"state"`
	Status       string               `json:"status"`
	LastSeenAt   int64                `json:"last_seen_at"`
	LastError    string               `json:"last_error"`
	Revision     int64                `json:"revision"`
	Directory    *agentlink.Directory `json:"directory,omitempty"`
}

type potMCPLive struct {
	cancel context.CancelFunc
	client *mcpreceiver.Client
	online bool
	done   chan struct{}
}

type potMCPConnections struct {
	mu   sync.Mutex
	live map[string]*potMCPLive
	wg   sync.WaitGroup
}

func (s *Service) PotMCPConnection(ctx context.Context, id string) (PotMCPConnection, error) {
	var c PotMCPConnection
	var raw string
	e := s.integrationDB.QueryRowContext(ctx, `SELECT id,url,node_id,remote_node_id,state,last_seen_at,last_error,revision,directory FROM mcp_receiver_connections WHERE id=? AND node_id=?`, id, s.options.RuntimeID).Scan(&c.ID, &c.URL, &c.NodeID, &c.RemoteNodeID, &c.State, &c.LastSeenAt, &c.LastError, &c.Revision, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return s.inboundPotMCPConnection(ctx, id)
	}
	if e != nil {
		return c, e
	}
	if raw != "" {
		var d agentlink.Directory
		if e = json.Unmarshal([]byte(raw), &d); e != nil {
			return c, e
		}
		c.Directory = &d
	}
	c.Status = c.State
	if c.State == "active" {
		c.Status = "offline"
		s.potMCP.mu.Lock()
		live := s.potMCP.live[id]
		if live != nil && live.online && c.LastSeenAt > time.Now().Add(-45*time.Second).UnixMilli() {
			c.Status = "connected"
		}
		s.potMCP.mu.Unlock()
	}
	return c, nil
}
func (s *Service) PotMCPConnections(ctx context.Context) ([]PotMCPConnection, error) {
	rows, e := s.integrationDB.QueryContext(ctx, "SELECT id FROM mcp_receiver_connections WHERE node_id=? ORDER BY id", s.options.RuntimeID)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	result := []PotMCPConnection{}
	for _, id := range ids {
		c, e := s.PotMCPConnection(ctx, id)
		if e != nil {
			return nil, e
		}
		result = append(result, c)
	}
	return result, nil
}
func (s *Service) potMCPGrant(ctx context.Context, id string) (PotMCPConnection, potMCPToken, error) {
	c, e := s.PotMCPConnection(ctx, id)
	var token potMCPToken
	if e != nil {
		return c, token, e
	}
	var ref string
	if e = s.integrationDB.QueryRowContext(ctx, "SELECT secret_ref FROM mcp_receiver_connections WHERE id=? AND node_id=?", id, s.options.RuntimeID).Scan(&ref); e != nil {
		return c, token, e
	}
	raw, e := s.integrationSecrets.Get(ctx, ref)
	if e != nil {
		return c, token, e
	}
	defer clear(raw)
	if e = json.Unmarshal(raw, &token); e != nil || token.SchemaVersion != 1 {
		return c, token, agentlink.ErrInvalid
	}
	return c, token, nil
}
func (s *Service) DisconnectPotMCP(ctx context.Context, id string) error {
	result, e := s.integrationDB.ExecContext(ctx, `UPDATE mcp_receiver_connections SET state='disconnected',revision=revision+1 WHERE id=? AND node_id=? AND state!='revoked'`, id, s.options.RuntimeID)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return agentlink.ErrNotFound
	}
	s.stopPotMCP(id)
	s.notifyMCPConnections()
	return nil
}
func (s *Service) ReconnectPotMCP(ctx context.Context, id string) error {
	c, token, e := s.potMCPGrant(ctx, id)
	if e != nil {
		return e
	}
	if c.State == "revoked" || !token.ExpiresAt.After(time.Now()) {
		return agentlink.ErrUnauthorized
	}
	_, e = s.integrationDB.ExecContext(ctx, `UPDATE mcp_receiver_connections SET state='active',last_error='',revision=revision+1 WHERE id=? AND node_id=? AND state!='revoked'`, id, s.options.RuntimeID)
	if e == nil {
		s.startPotMCP(id)
		s.notifyMCPConnections()
	}
	return e
}
func (s *Service) RevokePotMCP(ctx context.Context, id string) error {
	if c, e := s.inboundPotMCPConnection(ctx, id); e == nil && c.Inbound {
		a := s.localPotMCPAccess()
		if a == nil {
			return agentlink.ErrUnavailable
		}
		e = a.options.OAuth.Revoke(ctx, id)
		s.notifyMCPConnections()
		return e
	}

	c, token, e := s.potMCPGrant(ctx, id)
	if e != nil {
		return e
	}
	// Pause first; a failed revocation is visible and does not claim remote success.
	if c.State != "revoked" {
		if e = s.DisconnectPotMCP(ctx, id); e != nil {
			return e
		}
	}
	metadata, e := discoverPotMCP(ctx, c.URL)
	if e != nil {
		return e
	}
	values := url.Values{"token": {token.AccessToken}, "client_id": {token.ClientID}}
	if e = potMCPJSON(ctx, potMCPHTTP(), "POST", metadata.Revocation, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()), nil); e != nil {
		return e
	}
	_, e = s.integrationDB.ExecContext(ctx, `UPDATE mcp_receiver_connections SET state='revoked',last_error='',revision=revision+1 WHERE id=? AND node_id=?`, id, s.options.RuntimeID)
	s.notifyMCPConnections()
	return e
}

func (s *Service) savePotMCPDirectory(ctx context.Context, id string, d agentlink.Directory) error {
	if !agentlink.NodeTarget(d.NodeID).Valid() || d.NodeID == s.options.RuntimeID || d.Adapter != "pot" {
		return agentlink.ErrUnauthorized
	}
	// Directory validation also rejects owner scope, duplicate addresses and forged
	// identity metadata; it never creates or selects a local assistant.
	if e := validatePotMCPDirectory(d); e != nil {
		return e
	}
	return storage.Transaction(ctx, s.integrationDB, func(tx *sql.Tx) error {
		var remote, state string
		if e := tx.QueryRowContext(ctx, "SELECT remote_node_id,state FROM mcp_receiver_connections WHERE id=? AND node_id=?", id, s.options.RuntimeID).Scan(&remote, &state); e != nil {
			return e
		}
		if state != "active" || remote != "" && remote != d.NodeID {
			return agentlink.ErrUnauthorized
		}
		_, e := tx.ExecContext(ctx, `UPDATE mcp_receiver_connections SET remote_node_id=?,directory=?,last_seen_at=?,last_error='' WHERE id=?`, d.NodeID, mcpJSON(d), time.Now().UnixMilli(), id)
		return e
	})
}

// PotMCPReceiverCapabilities lists only the operations implemented by the local
// Pot receiver. File paths are metadata; this transport advertises no file bytes.
func PotMCPReceiverCapabilities() agentlink.Description {
	return agentlink.Description{Version: 1, Scope: "peer", Operations: []agentlink.Operation{agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput, agentlink.TaskEvents, agentlink.ChatSend, agentlink.ChatRead, agentlink.ChatReceipt, agentlink.Approvals}}
}
func (s *Service) potMCPRegistrations(ctx context.Context) ([]agentlink.MCPReceiverRegistration, error) {
	defs, e := s.Registry.Definitions(ctx)
	if e != nil {
		return nil, e
	}
	result := []agentlink.MCPReceiverRegistration{{Version: agentlink.MCPContractVersion, Receiver: agentlink.Identity{Address: agentlink.NodeTarget(s.options.RuntimeID), Name: "Pot", Adapter: "pot"}, Capabilities: PotMCPNodeCapabilities()}}
	for _, d := range defs {
		identity, e := s.agentLinkIdentity(ctx, d.ID)
		if e != nil {
			return nil, e
		}
		identity.Avatar = d.Avatar
		result = append(result, agentlink.MCPReceiverRegistration{Version: agentlink.MCPContractVersion, Receiver: identity, Capabilities: PotMCPReceiverCapabilities(), Epoch: s.options.BootID})
	}
	return result, nil
}
func (s *Service) stopPotMCP(id string) {
	s.potMCP.mu.Lock()
	live := s.potMCP.live[id]
	if live != nil {
		live.online = false
		live.cancel()
	}
	s.potMCP.mu.Unlock()
	if live != nil {
		<-live.done
	}
}

func (s *Service) startPotMCP(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.potMCP.mu.Lock()
	if s.potMCP.live == nil {
		s.potMCP.live = map[string]*potMCPLive{}
	}
	if s.potMCP.live[id] != nil {
		s.potMCP.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	live := &potMCPLive{cancel: cancel, done: make(chan struct{})}
	s.potMCP.live[id] = live
	s.potMCP.wg.Add(1)
	s.potMCP.mu.Unlock()
	go func() {
		defer s.potMCP.wg.Done()
		defer close(live.done)
		defer func() {
			cancel()
			s.potMCP.mu.Lock()
			if s.potMCP.live[id] == live {
				delete(s.potMCP.live, id)
			}
			s.potMCP.mu.Unlock()
		}()
		s.runPotMCP(ctx, id, live)
	}()
}
func (s *Service) resumePotMCP(ctx context.Context) error {
	rows, e := s.integrationDB.QueryContext(ctx, "SELECT id FROM mcp_receiver_connections WHERE node_id=? AND state='active'", s.options.RuntimeID)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		s.startPotMCP(id)
	}
	return nil
}
func potMCPPause(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func potMCPPermanent(e error) bool {
	var problem agentlink.MCPError
	if errors.As(e, &problem) {
		return problem.Retry() == agentlink.MCPDoNotRetry
	}
	return errors.Is(e, agentlink.ErrUnauthorized) || errors.Is(e, agentlink.ErrInvalid) || errors.Is(e, agentlink.ErrConflict) || errors.Is(e, agentlink.ErrFenced) || errors.Is(e, agentlink.ErrUnsupported)
}

func validatePotMCPDirectory(d agentlink.Directory) error {
	if len(d.Targets) > 257 || d.Name == "" || d.DefaultTarget != nil {
		return agentlink.ErrInvalid
	}
	seen := map[protocol.AgentRef]bool{}
	for _, endpoint := range d.Targets {
		ref := endpoint.Address
		if !agentlink.ValidTarget(ref) || seen[ref] || endpoint.Name == "" || endpoint.Adapter == "" || endpoint.Capabilities.Version != 1 || len(endpoint.Capabilities.Operations) > 64 {
			return agentlink.ErrInvalid
		}
		seen[ref] = true
		scope := "peer"
		if ref.Kind == "node" {
			scope = "caller"
		}
		if endpoint.Capabilities.Scope != scope {
			return agentlink.ErrUnauthorized
		}
		ops := map[agentlink.Operation]bool{}
		for _, op := range endpoint.Capabilities.Operations {
			if ops[op] || !PotMCPReceiverCapabilities().Supports(op) {
				return agentlink.ErrUnsupported
			}
			ops[op] = true
		}
	}
	return nil
}

// PotMCPNodeCapabilities registers identity without offering a default executor.
func PotMCPNodeCapabilities() agentlink.Description {
	return agentlink.Description{Version: 1, Scope: "caller", Operations: []agentlink.Operation{}}
}

// PotMCPConnectionView includes incoming registrations without treating their
// saved catalogue as a live transport or creating an outbound configuration.
func (s *Service) PotMCPConnectionView(ctx context.Context) ([]PotMCPConnection, error) {
	outgoing, e := s.PotMCPConnections(ctx)
	if e != nil {
		return nil, e
	}
	incoming, e := s.inboundPotMCPConnections(ctx)
	if e != nil {
		return nil, e
	}
	return append(outgoing, incoming...), nil
}

func (s *Service) notifyMCPConnections() {
	s.Lifecycle.Publish(protocol.LifecycleEvent{Kind: "authority_changed"})
	s.Changes.Notify("agent-links")
}
