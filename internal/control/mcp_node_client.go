package control

import (
	"context"
	"time"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
)

func (s *Service) runPotMCP(ctx context.Context, id string, live *potMCPLive) {
	for ctx.Err() == nil {
		c, token, e := s.potMCPGrant(ctx, id)
		if e != nil || c.State != "active" {
			return
		}
		if !token.ExpiresAt.After(time.Now()) {
			_, _ = s.integrationDB.ExecContext(ctx, "UPDATE mcp_receiver_connections SET last_error='authorization_expired' WHERE id=? AND state='active'", id)
			return
		}
		identity, e := agentlink.NewMCPReceiver(id, s.options.RuntimeID)
		if e != nil {
			return
		}
		client, e := mcpreceiver.Connect(ctx, c.URL, token.AccessToken, identity)
		token.AccessToken = ""
		if e == nil {
			registrations, err := s.potMCPRegistrations(ctx)
			e = err
			for _, registration := range registrations {
				if e != nil {
					break
				}
				e = client.Register(ctx, registration)
			}
			if e == nil {
				e = s.syncPotMCPDirectory(ctx, id, client)
			}
			if e == nil {
				s.potMCP.mu.Lock()
				live.client = client
				live.online = true
				s.potMCP.mu.Unlock()
				s.notifyMCPConnections()
			}
			backend := &potMCPReceiver{s: s, identity: identity, remote: client}
			nextDirectory := time.Now().Add(15 * time.Second)
			for e == nil && ctx.Err() == nil {
				e = mcpreceiver.Step(ctx, client, backend)
				if e == nil {
					e = s.endPotMCPExecutions(ctx, id)
				}
				if e == nil {
					fresh, err := s.PotMCPConnection(ctx, id)
					e = err
					if e == nil {
						e = s.syncPotMCPMail(ctx, fresh, client)
						if e == nil {
							s.syncPotMCPChats(ctx, fresh)
						}
					}
				}
				if e == nil && time.Now().After(nextDirectory) {
					e = s.syncPotMCPDirectory(ctx, id, client)
					nextDirectory = time.Now().Add(15 * time.Second)
				}
			}
			s.potMCP.mu.Lock()
			live.online = false
			live.client = nil
			s.potMCP.mu.Unlock()
			client.Close()
			s.notifyMCPConnections()
		}
		if ctx.Err() != nil {
			return
		}
		failure := mcpFailure(e)
		code := string(failure.Code)
		if !token.ExpiresAt.After(time.Now()) {
			code = "authorization_expired"
		}
		_, _ = s.integrationDB.ExecContext(ctx, "UPDATE mcp_receiver_connections SET last_error=? WHERE id=?", code, id)
		if potMCPPermanent(e) {
			return
		}
		if !potMCPPause(ctx) {
			return
		}
	}
}
func (s *Service) syncPotMCPDirectory(ctx context.Context, id string, client *mcpreceiver.Client) error {
	var directory agentlink.Directory
	if e := client.Call(ctx, "pot_directory", struct{}{}, &directory); e != nil {
		return e
	}
	targets := []agentlink.Endpoint{}
	for _, endpoint := range directory.Targets {
		if endpoint.Address.NodeID == directory.NodeID {
			targets = append(targets, endpoint)
		}
	}
	directory.Targets = targets
	return s.savePotMCPDirectory(ctx, id, directory)
}
