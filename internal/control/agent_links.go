package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/peer"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

type agentLinkConfig struct {
	Listen   string `json:"listen"`
	Endpoint string `json:"endpoint"`
	Mode     string `json:"mode"`
}

type agentLinkHost struct {
	mu           sync.Mutex
	service      *agentlink.Service
	config       agentLinkConfig
	server       *linkhost.PeerListener
	setup        sync.Mutex
	networkError string
	registry     *linkhost.Registry
	discoveryMu  sync.Mutex
	discoveryAt  time.Time
	discovered   []linkhost.Address
	additional   map[string]*linkhost.PeerListener
}

type controlLinkMailbox struct{ s *Service }

func (m controlLinkMailbox) PendingNode(ctx context.Context, local, peer string) ([]protocol.Envelope, error) {
	return m.s.Mailbox.PendingNode(ctx, local, peer)
}
func (m controlLinkMailbox) Deliver(ctx context.Context, value protocol.Envelope) (protocol.Receipt, error) {
	direct, err := m.s.isLinkChatReply(ctx, value)
	if err != nil {
		return protocol.Receipt{}, err
	}
	if !direct {
		if value.To.Kind != "assistant" {
			return protocol.Receipt{}, agentlink.ErrNotFound
		}
		if _, err := m.s.agentLinkIdentity(ctx, value.To.AgentID); err != nil {
			return protocol.Receipt{}, err
		}
	}
	var receipt protocol.Receipt
	if direct {
		receipt, err = m.s.Mailbox.DeliverChat(ctx, value)
	} else {
		receipt, err = m.s.Mailbox.Deliver(ctx, value)
	}
	if err == nil && direct {
		err = m.s.captureLinkChat(ctx, value)
	}
	if err == nil {
		m.s.wakeTasks()
		m.s.Changes.Notify("assistant-mail")
	}
	return receipt, err
}
func (m controlLinkMailbox) Acknowledge(ctx context.Context, value protocol.Receipt) error {
	return m.s.Mailbox.Acknowledge(ctx, value)
}

func (s *Service) agentLinkIdentity(ctx context.Context, id string) (agentlink.Identity, error) {
	if id == "" {
		return agentlink.Identity{Address: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "node", AgentID: s.options.RuntimeID}, Name: "Pot", Adapter: "pot"}, nil
	}
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return agentlink.Identity{}, err
	}
	for _, d := range definitions {
		if d.ID == id {
			return agentlink.Identity{Address: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: id}, Name: d.Name, Adapter: "pot"}, nil
		}
	}
	return agentlink.Identity{}, protocol.APIError{Code: "agent_link_assistant_unavailable"}
}

func (s *Service) openAgentLinks() error {
	hostIdentity, hostKey := s.Machines.TransportIdentity()
	service, err := agentlink.New(agentlink.Options{HostIdentity: hostIdentity, HostKey: hostKey, DB: s.integrationDB, Backend: potLinkBackend{s}, Secrets: s.integrationSecrets, Identity: s.agentLinkIdentity, Mailbox: controlLinkMailbox{s}, Changed: func() { s.Changes.Notify("agent-links"); s.Changes.Notify("assistant-mail"); s.wakeTasks() }})
	if err != nil {
		return err
	}
	host := &agentLinkHost{service: service, config: agentLinkConfig{Listen: "127.0.0.1:0", Mode: "address"}}
	err = s.integrationDB.QueryRowContext(s.ctx, "SELECT listen,endpoint,mode FROM agent_link_host WHERE singleton=1").Scan(&host.config.Listen, &host.config.Endpoint, &host.config.Mode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if s.options.PeerListen != "" {
		host.config.Listen = s.options.PeerListen
	}
	host.registry = linkhost.NewRegistry(hostIdentity, hostKey)
	if err = host.registry.Register(agentlink.ServiceName, func(ctx context.Context, remote peer.Identity, name string, auth json.RawMessage) (peer.Handler, error) {
		return service.Authorize(ctx, remote, name, auth)
	}, service.Attach); err != nil {
		return err
	}
	s.agentLinks = host
	if err := s.restoreChatReplyBindings(s.ctx); err != nil {
		return err
	}
	s.pluginWG.Add(1)
	go func() {
		defer s.pluginWG.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.syncTaskExecutions(s.ctx)
			}
		}
	}()
	s.pluginWG.Add(1)
	go func() {
		defer s.pluginWG.Done()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.syncLinkChats(s.ctx)
			}
		}
	}()
	if err = s.linkPlugins.RegisterAgent(linkhost.Messages{Name: "pot", Service: service}); err != nil {
		return err
	}
	if err = s.startAgentListener(host.config); err != nil {
		if host.config.Endpoint == "" {
			return err
		}
		fallback := host.config
		fallback.Listen = "127.0.0.1:0"
		if err = s.startAgentListener(fallback); err != nil {
			return err
		}
		host.networkError = "agent_link_listener_unavailable"
	}
	if err = s.restoreAgentAddresses(s.ctx); err != nil {
		return err
	}
	s.pluginWG.Add(1)
	go func() { defer s.pluginWG.Done(); s.linkPlugins.Run(s.ctx) }()
	return nil
}

func validAgentListen(value string) bool {
	host, port, err := net.SplitHostPort(value)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || ip.IsUnspecified() {
		return false
	}
	if port == "0" {
		return ip.IsLoopback()
	}
	_, err = peer.ValidateEndpoint(context.Background(), "tcp://"+value)
	return err == nil
}

func (s *Service) installAgentListener(ctx context.Context, config agentLinkConfig, server *linkhost.PeerListener) error {
	config.Listen = server.Addr()
	_, err := s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_host(singleton,listen,endpoint,mode) VALUES(1,?,?,?) ON CONFLICT(singleton) DO UPDATE SET listen=excluded.listen,endpoint=excluded.endpoint,mode=excluded.mode", config.Listen, config.Endpoint, config.Mode)
	if err != nil {
		return err
	}
	host := s.agentLinks
	host.mu.Lock()
	if err := s.ctx.Err(); err != nil {
		host.mu.Unlock()
		return err
	}
	old := host.server
	host.server = server
	host.config = config
	host.networkError = ""
	host.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}
func (s *Service) startAgentListener(config agentLinkConfig) error {
	if !validAgentListen(config.Listen) {
		return protocol.APIError{Code: "agent_link_invalid_listen"}
	}
	server, err := linkhost.ListenPeer(s.ctx, config.Listen, s.agentLinks.registry.Server())
	if err != nil {
		return protocol.APIError{Code: "agent_link_listener_unavailable"}
	}
	if err = s.installAgentListener(s.ctx, config, server); err != nil {
		server.Close()
	}
	return err
}

func (s *Service) closeAgentLinks() error {
	if s.agentLinks == nil {
		return nil
	}
	s.agentLinks.mu.Lock()
	server := s.agentLinks.server
	s.agentLinks.mu.Unlock()
	s.agentLinks.setup.Lock()
	defer s.agentLinks.setup.Unlock()
	for _, listener := range s.agentLinks.additional {
		listener.Close()
	}
	if server != nil {
		return server.Close()
	}
	return nil
}

func (s *Service) AgentLinks(ctx context.Context) (any, error) {
	if s.agentLinks == nil {
		return nil, protocol.APIError{Code: "agent_link_unavailable"}
	}
	identity, _ := s.agentLinkIdentity(ctx, "")
	h := s.agentLinks
	h.mu.Lock()
	config := h.config
	networkError := h.networkError
	h.mu.Unlock()
	status, reason := "ready", ""
	if config.Endpoint == "" {
		status, reason = "needs_access", "agent_link_access_required"
	}
	if networkError != "" {
		status, reason = "needs_access", networkError
	}
	self := map[string]any{"id": identity.Address.AgentID, "node_id": s.options.RuntimeID, "kind": "pot", "name": identity.Name, "address": config.Endpoint, "endpoint": config.Endpoint, "status": status, "reason_code": reason}
	localDirectory, _ := (potLinkBackend{s}).Directory(ctx)
	self["name"] = localDirectory.Name
	localObservation, observationErr := (potLinkBackend{s}).Observe(ctx, "")
	localSample := agentlink.ObservationSample{ObservedAtMS: time.Now().UnixMilli()}
	if observationErr == nil {
		localSample.Value = &localObservation
	} else {
		localSample.Error = "agent_link_observation_unavailable"
	}
	self["observation"] = localSample
	connections, err := h.service.List(ctx)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, c := range connections {
		if c.State == "revoked" {
			continue
		}
		state := c.State
		if state == "ready" || state == "active" {
			state = "connected"
		}
		peerName := c.Peer.Name
		localOutbox := agentlink.MessageQueue{}
		localOutbox.Pending, localOutbox.OldestAtMS, err = inbox.OutboxStats(ctx, s.Tasks.DB(), "out", s.options.RuntimeID, c.PeerHost.NodeID)
		if err != nil {
			return nil, err
		}
		assistants := []any{}
		if c.Directory != nil {
			for _, a := range c.Directory.Assistants() {
				var count int64
				var replyID, preview string
				var at int64
				if e := s.integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_link_chat WHERE link_id=? AND assistant_id=? AND role='assistant'", c.ID, a.Address.AgentID).Scan(&count); e != nil {
					return nil, e
				}
				e := s.integrationDB.QueryRowContext(ctx, "SELECT id,text,created_at FROM agent_link_chat WHERE link_id=? AND assistant_id=? AND role='assistant' ORDER BY rowid DESC LIMIT 1", c.ID, a.Address.AgentID).Scan(&replyID, &preview, &at)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return nil, e
				}
				capabilities, err := s.AgentCapabilities(ctx, "agent-link:"+c.ID+":"+a.Address.AgentID)
				if err != nil {
					return nil, err
				}
				assistants = append(assistants, map[string]any{"address": a.Address, "name": a.Name, "avatar": a.Avatar, "adapter": a.Adapter, "capabilities": capabilities, "reply_count": count, "reply_id": replyID, "preview": preview, "last_reply_at_ms": at})
			}
			peerName = c.Directory.Name
		}
		lastExchange := int64(0)
		if c.LastSeen != nil {
			lastExchange = c.LastSeen.UnixMilli()
		}
		items = append(items, map[string]any{"assistants": assistants, "directory": c.Directory, "id": c.ID, "peer": map[string]any{"id": c.Peer.Address.AgentID, "node_id": c.Peer.Address.NodeID, "kind": c.Peer.Adapter, "name": peerName, "address": c.Endpoint}, "status": state, "revision": c.CreatedAt.UnixMilli(), "last_error": c.LastError, "last_exchange_at_ms": lastExchange, "outbox": localOutbox, "observation": h.service.Observation(c.ID)})
	}
	addresses, err := s.agentAddresses(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"self": self, "items": items, "addresses": addresses, "network": map[string]any{"listen": config.Listen, "endpoint": config.Endpoint, "mode": config.Mode, "status": status, "reason_code": reason}}, nil
}

type AgentLinkCommand struct {
	Action    string `json:"action"`
	Address   string `json:"address"`
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	RequestID string `json:"request_id"`
	Listen    string `json:"listen"`
	Endpoint  string `json:"endpoint"`
	Mode      string `json:"mode"`
}

func agentLinkError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{agentlink.ErrAssistantBusy, agentlink.ErrAssistantChanged, agentlink.ErrInvalid, agentlink.ErrNotFound, agentlink.ErrUnauthorized, agentlink.ErrExpired, agentlink.ErrUsed, agentlink.ErrConflict, agentlink.ErrRevoked, agentlink.ErrReconnectRequired, agentlink.ErrUnavailable, agentlink.ErrUnsupported, agentlink.ErrDisconnected, agentlink.ErrCapacity, agentlink.ErrFenced} {
		if errors.Is(err, known) {
			return protocol.APIError{Code: known.Error()}
		}
	}
	var api protocol.APIError
	if errors.As(err, &api) {
		return err
	}
	return protocol.APIError{Code: "agent_link_failed"}
}

func (s *Service) ManageAgentLink(ctx context.Context, command AgentLinkCommand) (any, error) {
	if s.agentLinks == nil {
		return nil, protocol.APIError{Code: "agent_link_unavailable"}
	}
	var result any
	var err error
	switch command.Action {
	case "invite":
		s.agentLinks.mu.Lock()
		endpoint := s.agentLinks.config.Endpoint
		s.agentLinks.mu.Unlock()
		if command.Endpoint != "" {
			endpoint, err = s.selectAgentAddress(ctx, command.Endpoint)
			if err != nil {
				return nil, agentLinkError(err)
			}
		}
		if endpoint == "" {
			return nil, protocol.APIError{Code: "agent_link_access_required"}
		}
		invite, e := s.agentLinks.service.Invite(ctx, endpoint)
		err = e
		result = map[string]any{"address": invite.Address, "expires_at_ms": invite.ExpiresAt.UnixMilli()}
	case "preview":
		preview, e := s.agentLinks.service.Preview(ctx, command.Address)
		if e != nil {
			return nil, agentLinkError(e)
		}
		return map[string]any{
			"peer":     map[string]any{"id": preview.Identity.Address.AgentID, "node_id": preview.Identity.Address.NodeID, "kind": preview.Identity.Adapter, "name": preview.Identity.Name},
			"endpoint": preview.Endpoint, "fingerprint": preview.Fingerprint, "expires_at_ms": preview.ExpiresAt.UnixMilli(),
		}, nil
	case "connect":
		result, err = s.agentLinks.service.Connect(ctx, command.Address)
	case "disconnect":
		var current agentlink.Connection
		current, err = s.agentLinks.service.Get(ctx, command.ID)
		if err == nil && command.Revision != 0 && current.CreatedAt.UnixMilli() != command.Revision {
			return nil, protocol.APIError{Code: "agent_link_identity_conflict"}
		}
		if err == nil {
			err = s.agentLinks.service.Disconnect(ctx, command.ID)
		}
		result = map[string]bool{"disconnected": err == nil}
	case "reconnect":
		err = s.agentLinks.service.Reconnect(ctx, command.ID)
		result = map[string]bool{"connected": err == nil}
	case "delete":
		err = s.agentLinks.service.Delete(ctx, command.ID)
		result = map[string]bool{"deleted": err == nil}
	case "configure":
		result, err = s.configureAgentAccess(ctx, command)
	default:
		err = agentlink.ErrInvalid
	}
	if err == nil {
		s.Changes.Notify("agent-links")
	}
	return result, agentLinkError(err)
}

func (s *Service) configureAgentAccess(ctx context.Context, command AgentLinkCommand) (any, error) {
	h := s.agentLinks
	h.setup.Lock()
	defer h.setup.Unlock()
	h.mu.Lock()
	config := h.config
	h.mu.Unlock()
	if command.Listen != "" && command.Listen != config.Listen {
		return nil, protocol.APIError{Code: "agent_link_listener_restart_required"}
	}
	if command.Mode != "" && command.Mode != "address" {
		return nil, agentlink.ErrInvalid
	}
	config.Mode = "address"
	config.Endpoint = strings.TrimRight(command.Endpoint, "/")
	endpoint, err := peer.ValidateEndpoint(ctx, config.Endpoint)
	if err != nil {
		return nil, agentlink.ErrInvalid
	}
	config.Endpoint = endpoint
	var candidate *linkhost.PeerListener
	if err = s.probeAgentEndpoint(ctx, endpoint); err != nil {
		var unavailable protocol.APIError
		if !errors.As(err, &unavailable) || unavailable.Code != "agent_link_unreachable" {
			return nil, err
		}
		listen, e := linkhost.TCPListenAddress(ctx, endpoint)
		if e != nil || listen == "" || listen == config.Listen {
			return nil, err
		}
		candidate, e = linkhost.ListenPeer(s.ctx, listen, h.registry.Server())
		if e != nil {
			return nil, protocol.APIError{Code: "agent_link_listener_unavailable"}
		}
		defer func() {
			if candidate != nil {
				candidate.Close()
			}
		}()
		if err = s.probeAgentEndpoint(ctx, endpoint); err != nil {
			return nil, err
		}
	}
	if candidate != nil {
		if err = s.installAgentListener(ctx, config, candidate); err != nil {
			return nil, err
		}
		candidate = nil
	} else {
		_, err = s.integrationDB.ExecContext(ctx, "UPDATE agent_link_host SET endpoint=?,mode=? WHERE singleton=1", config.Endpoint, config.Mode)
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.config = config
		h.networkError = ""
		h.mu.Unlock()
	}
	return s.AgentLinks(ctx)
}

func (s *Service) probeAgentEndpoint(ctx context.Context, endpoint string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	identity, key := s.Machines.TransportIdentity()
	if err := linkhost.Probe(ctx, endpoint, identity, key, identity); err != nil {
		if errors.Is(err, peer.ErrUnauthorized) {
			return protocol.APIError{Code: "agent_link_identity_conflict"}
		}
		return protocol.APIError{Code: "agent_link_unreachable"}
	}
	return nil
}

func (s *Service) linkedPeer(ctx context.Context, local, peer protocol.AgentRef) bool {
	if s.agentLinks == nil {
		return false
	}
	links, err := s.agentLinks.service.List(ctx)
	if err != nil {
		return false
	}
	for _, link := range links {
		if link.State == "revoked" || link.State == "disconnected" || link.LastSeen == nil || link.Local.Address.NodeID != local.NodeID || link.Peer.Address.NodeID != peer.NodeID {
			continue
		}
		d := link.Directory
		if d == nil {
			fresh, e := s.agentLinks.service.Directory(ctx, link.ID)
			if e != nil {
				continue
			}
			d = &fresh
		}
		for _, a := range d.Assistants() {
			if a.Address == peer {
				return true
			}
		}
	}
	return false
}

func (s *Service) agentLinkPeers(ctx context.Context) ([]protocol.MailAssistant, error) {
	result := []protocol.MailAssistant{}
	if s.agentLinks == nil {
		return result, nil
	}
	links, err := s.agentLinks.service.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.State != "revoked" && link.State != "disconnected" && link.LastSeen != nil {
			if link.Directory == nil {
				d, e := s.agentLinks.service.Directory(ctx, link.ID)
				if e == nil {
					link.Directory = &d
				}
			}
			if link.Directory != nil {
				for _, a := range link.Directory.Assistants() {
					result = append(result, protocol.MailAssistant{Address: a.Address, Name: a.Name})
				}
			}
		}
	}
	return result, nil
}

// PeerListenAddress returns the actual TCP listener without publishing an invite.
func (s *Service) PeerListenAddress() string {
	if s.agentLinks == nil {
		return ""
	}
	s.agentLinks.mu.Lock()
	defer s.agentLinks.mu.Unlock()
	return s.agentLinks.server.Addr()
}
