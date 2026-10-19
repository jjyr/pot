package controlplane

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
)

const MembershipService = "runner-membership"
const ExecutionService = "execution"

type executionAuth struct {
	Epoch uint64 `json:"epoch"`
	Token string `json:"token"`
}

func (d *Directory) TransportIdentity() (peer.Identity, ed25519.PrivateKey) {
	return peer.Identity{NodeID: d.identity.RuntimeID, PublicKey: d.identity.PublicKey}, append(ed25519.PrivateKey(nil), d.key...)
}
func (i Identity) TransportIdentity() peer.Identity {
	return peer.Identity{NodeID: i.RuntimeID, PublicKey: i.PublicKey}
}

// Authorize scopes a signed TCP session to either enrollment or an existing
// execution membership. Neither service can enter owner HTTP management.
func (s *Server) Authorize(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
	d := s.Directory
	if d == nil || d.options.Embedded || d.Role() != "control" {
		return nil, ErrUnauthorized
	}
	switch service {
	case MembershipService:
		return func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
			switch request.Method {
			case "preview":
				return json.Marshal(d.Identity())
			case "join":
				var signed Signed
				var join JoinRequest
				if core.DecodeJSON(request.Params, &signed) != nil || core.DecodeJSON(signed.Payload, &join) != nil || join.Caller.TransportIdentity() != remote {
					return nil, ErrUnauthorized
				}
				result, err := d.Join(ctx, signed)
				if err != nil {
					return nil, err
				}
				if s.OnEnroll != nil {
					var enrollment Enrollment
					if core.DecodeJSON(result.Payload, &enrollment) != nil {
						return nil, ErrUnauthorized
					}
					d.mu.Lock()
					member := d.state.Members[enrollment.RuntimeID]
					d.mu.Unlock()
					if err = s.OnEnroll(ctx, member.Machine); err != nil {
						return nil, err
					}
				}
				return json.Marshal(result)
			default:
				return nil, ErrUnauthorized
			}
		}, nil
	case ExecutionService:
		principal, err := s.executionPrincipal(remote, auth)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
			current, err := s.executionPrincipal(remote, auth)
			if err != nil || current != principal {
				return nil, ErrUnauthorized
			}
			return executionHandler(s.Handler, principal)(ctx, request)
		}, nil
	default:
		return nil, ErrUnauthorized
	}
}
func (s *Server) executionPrincipal(remote peer.Identity, raw json.RawMessage) (Principal, error) {
	var auth executionAuth
	if core.DecodeJSON(raw, &auth) != nil {
		return Principal{}, ErrUnauthorized
	}
	d := s.Directory
	d.mu.Lock()
	defer d.mu.Unlock()
	member, ok := d.state.Members[remote.NodeID]
	if d.state.Role != "control" || !ok || !member.Machine.Enabled || member.Machine.Epoch != auth.Epoch || member.Machine.Identity.TransportIdentity() != remote || !secureToken(auth.Token, member.Token) {
		return Principal{}, ErrUnauthorized
	}
	return Principal{RuntimeID: remote.NodeID, Epoch: auth.Epoch}, nil
}
func (s *Server) Attach(session *peer.Session) {
	if session.Service() != ExecutionService {
		return
	}
	principal, err := s.executionPrincipal(session.Remote(), session.Auth())
	if err != nil {
		session.Close()
		return
	}
	d := s.Directory
	p := &Peer{session: session}
	d.mu.Lock()
	member, ok := d.state.Members[principal.RuntimeID]
	if !ok || !member.Machine.Enabled || member.Machine.Epoch != principal.Epoch {
		d.mu.Unlock()
		session.Close()
		return
	}
	old := d.peers[principal.RuntimeID]
	d.peers[principal.RuntimeID] = p
	d.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if s.OnPresence != nil {
		s.OnPresence(principal.RuntimeID, true)
	}
	go func() {
		<-session.Done()
		d.mu.Lock()
		current := d.peers[principal.RuntimeID] == p
		if current {
			delete(d.peers, principal.RuntimeID)
		}
		d.mu.Unlock()
		if current && s.OnPresence != nil {
			s.OnPresence(principal.RuntimeID, false)
		}
	}()
}

// PeerServer is useful to hosts composing only Runner membership. The complete
// Control host registers these same services beside Agent messages and identity.
func (s *Server) PeerServer() *peer.Server {
	identity, key := s.Directory.TransportIdentity()
	return &peer.Server{Identity: identity, PrivateKey: key, Authorize: func(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
		if service == "identity" {
			return func(_ context.Context, request peer.Request) (json.RawMessage, error) {
				if request.Method != "identity" {
					return nil, ErrUnauthorized
				}
				return json.Marshal(identity)
			}, nil
		}
		return s.Authorize(ctx, remote, service, auth)
	}, OnSession: s.Attach}
}

type Connector struct {
	Directory *Directory
	Handler   Handler
}

func (c *Connector) dial(ctx context.Context, endpoint string, pinned *Identity, service string, auth json.RawMessage, handler peer.Handler) (*peer.Session, error) {
	if c.Directory == nil {
		return nil, fmt.Errorf("Directory required")
	}
	identity, key := c.Directory.TransportIdentity()
	var expected *peer.Identity
	if pinned != nil {
		if err := pinned.Validate(); err != nil {
			return nil, err
		}
		value := pinned.TransportIdentity()
		expected = &value
	}
	return peer.Dial(ctx, endpoint, peer.DialOptions{Identity: identity, PrivateKey: key, Expected: expected, Service: service, Auth: auth, Handler: handler, Context: ctx})
}
func (c *Connector) Preview(ctx context.Context, endpoint string) (Identity, error) {
	session, err := c.dial(ctx, endpoint, nil, MembershipService, nil, nil)
	if err != nil {
		return Identity{}, err
	}
	defer session.Close()
	raw, err := session.Call(ctx, peer.Request{ID: "preview:" + secret(), Method: "preview", Params: json.RawMessage("null")})
	if err != nil {
		return Identity{}, err
	}
	var identity Identity
	if err = core.DecodeJSON(raw, &identity); err != nil {
		return Identity{}, err
	}
	if identity.Validate() != nil || identity.TransportIdentity() != session.Remote() {
		return Identity{}, ErrUnauthorized
	}
	return identity, nil
}

// Enroll retries the same durable signed join identity only. A transport-level
// uncertainty never creates another request or changes the original member.
func (c *Connector) Enroll(ctx context.Context, endpoint string, pinned Identity, invitation string, requestID ...string) (Membership, error) {
	if c.Directory == nil {
		return Membership{}, fmt.Errorf("Directory required")
	}
	signed, err := c.Directory.prepareJoin(endpoint, pinned, invitation, requestID...)
	if err != nil {
		return Membership{}, err
	}
	session, err := c.dial(ctx, endpoint, &pinned, MembershipService, nil, nil)
	if err != nil {
		return Membership{}, err
	}
	defer session.Close()
	data, _ := json.Marshal(signed)
	var original JoinRequest
	if core.DecodeJSON(signed.Payload, &original) != nil {
		return Membership{}, ErrUnauthorized
	}
	raw, err := session.Call(ctx, peer.Request{ID: "join:" + original.RequestID, Method: "join", Params: data})
	if err != nil {
		return Membership{}, err
	}
	var reply Signed
	var enrollment Enrollment
	if core.DecodeJSON(raw, &reply) != nil || verify(pinned, reply, &enrollment) != nil || enrollment.RequestID != original.RequestID || enrollment.Control != pinned {
		return Membership{}, ErrUnauthorized
	}
	membership := Membership{Origin: endpoint, Enrollment: enrollment}
	if err = c.Directory.SaveMembership(ctx, membership); err != nil {
		return Membership{}, err
	}
	return membership, nil
}

// Connect opens one outward authenticated session and never replays a call.
func (c *Connector) Connect(ctx context.Context) (*Peer, error) {
	if c.Directory == nil {
		return nil, fmt.Errorf("Directory required")
	}
	d := c.Directory
	d.mu.Lock()
	leaving := d.state.Leaving
	d.mu.Unlock()
	if leaving {
		return nil, fmt.Errorf("Runner is leaving; original leases require settlement")
	}
	member := d.Membership()
	if member == nil || d.Role() != "runner" {
		return nil, fmt.Errorf("enrolled Runner required")
	}
	if member.Origin == "" {
		return nil, protocol.APIError{Code: "runner_endpoint_required"}
	}
	auth, _ := json.Marshal(executionAuth{Epoch: member.Enrollment.Epoch, Token: member.Enrollment.Token})
	principal := Principal{RuntimeID: member.Enrollment.Control.RuntimeID, Epoch: member.Enrollment.Epoch}
	session, err := c.dial(ctx, member.Origin, &member.Enrollment.Control, ExecutionService, auth, executionHandler(c.Handler, principal))
	if err != nil {
		return nil, err
	}
	p := &Peer{session: session}
	d.mu.Lock()
	if d.state.Role != "runner" || d.state.Membership == nil || *d.state.Membership != *member || d.state.Leaving {
		d.mu.Unlock()
		p.Close()
		return nil, ErrUnauthorized
	}
	old := d.peers[member.Enrollment.Control.RuntimeID]
	d.peers[member.Enrollment.Control.RuntimeID] = p
	d.mu.Unlock()
	if old != nil {
		old.Close()
	}
	go func() {
		<-p.Done()
		d.mu.Lock()
		if d.peers[member.Enrollment.Control.RuntimeID] == p {
			delete(d.peers, member.Enrollment.Control.RuntimeID)
		}
		d.mu.Unlock()
	}()
	return p, nil
}
