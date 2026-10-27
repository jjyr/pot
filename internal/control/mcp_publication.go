package control

import (
	"context"
	"net/http"
	"net/url"

	"github.com/jjyr/pot/internal/mcppublic"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

type mcpBrowserSessionKey struct{}

// ServePublishedMCP is entered only after the private Control HTTP adapter has
// authenticated its owning browser process. External bearer/session metadata is
// restored solely for MCPAccess; it never enters the owner API dispatcher.
func (s *Service) ServePublishedMCP(w http.ResponseWriter, r *http.Request) {
	if !mcppublic.Route(r.URL.Path) || !mcppublic.Method(r.URL.Path, r.Method) || r.URL.RawPath != "" {
		http.NotFound(w, r)
		return
	}
	origin := r.Header.Get("X-Pot-MCP-Origin")
	u, e := url.Parse(origin)
	if e != nil || u.Host != r.Host {
		mcpOAuthError(w, 403, "invalid_request")
		return
	}
	s.mcpMountMu.Lock()

	if s.mcpPublications == nil {
		s.mcpPublications = map[string]*MCPAccess{}
	}
	access := s.mcpPublications[origin]
	if access == nil {
		options := MCPOAuthOptions{Origin: origin, LoginURL: "/mcp-connect.html", OwnerSession: func(r *http.Request) (string, bool) {
			session, _ := r.Context().Value(mcpBrowserSessionKey{}).(string)
			return session, session != ""
		}}
		if primary := s.localPotMCPAccess(); primary == nil {
			access, e = s.NewMCPAccess(options, publishedReceiverCapabilities)
		} else {
			options.DB = s.integrationDB
			options.NodeID = s.options.RuntimeID
			var oauth *MCPOAuthServer
			oauth, e = NewMCPOAuthServer(options)
			if e == nil {
				access, e = newMCPAccess(MCPAccessOptions{OAuth: oauth, Queue: primary.options.Queue, Local: s, ReceiverCapabilities: publishedReceiverCapabilities}, false)
			}
		}
		if e == nil {
			s.mcpPublications[origin] = access
		}
	}
	s.mcpMountMu.Unlock()
	if e != nil {
		mcpOAuthError(w, 503, "server_error")
		return
	}
	request := r.Clone(context.WithValue(r.Context(), mcpBrowserSessionKey{}, r.Header.Get("X-Pot-MCP-Owner")))
	request.Header.Set("Authorization", r.Header.Get("X-Pot-MCP-Authorization"))
	request.Header.Del("X-Pot-MCP-Owner")
	request.Header.Del("X-Pot-MCP-Authorization")
	request.Header.Del("X-Pot-MCP-Origin")
	access.ServeHTTP(w, request)
}

// The consented node owns its execution drivers and advertises their supported
// subset. The service bounds that subset to collaboration operations only; this
// policy does not grant owner APIs, file access or human decision authority.
func publishedReceiverCapabilities(_ context.Context, receiver agentlink.MCPReceiver, target protocol.AgentRef) (agentlink.Description, error) {
	if target.NodeID != receiver.NodeID() {
		return agentlink.Description{}, agentlink.ErrUnauthorized
	}
	if target.Kind == "node" {
		return agentlink.Description{Version: 1, Scope: "caller", Operations: []agentlink.Operation{agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput, agentlink.TaskEvents, agentlink.Approvals, agentlink.AssistantManagement}}, nil
	}
	if target.Kind != "assistant" {
		return agentlink.Description{}, agentlink.ErrUnsupported
	}
	return agentlink.Description{Version: 1, Scope: "peer", Operations: []agentlink.Operation{
		agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput, agentlink.TaskEvents,
		agentlink.ChatSend, agentlink.ChatRead, agentlink.ChatReceipt, agentlink.Approvals, agentlink.AssistantProfile, agentlink.AssistantDelete,
	}}, nil
}

// Each configured browser origin has its own OAuth audience and SDK sessions;
// they share the existing Control queues, identity and human permission store.
func (s *Service) closeMCPPublications() {
	s.mcpMountMu.Lock()
	defer s.mcpMountMu.Unlock()
	if len(s.mcpPublications) == 0 {
		if a := s.localPotMCPAccess(); a != nil {
			a.Close()
		}
		return
	}
	for _, a := range s.mcpPublications {
		a.Close()
	}
	s.mcpPublications = nil
}
