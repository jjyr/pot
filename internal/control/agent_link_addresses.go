package control

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
)

func (s *Service) agentAddresses(ctx context.Context) ([]linkhost.Address, error) {
	h := s.agentLinks
	h.discoveryMu.Lock()
	defer h.discoveryMu.Unlock()
	h.mu.Lock()
	config := h.config
	h.mu.Unlock()
	if time.Since(h.discoveryAt) > 10*time.Second {
		h.discovered = linkhost.DiscoverAddresses(ctx, config.Listen)
		h.discoveryAt = time.Now()
	}
	result := append([]linkhost.Address{}, h.discovered...)
	seen := map[string]bool{}
	for _, address := range result {
		seen[address.Endpoint] = true
	}
	rows, err := s.integrationDB.QueryContext(ctx, "SELECT endpoint FROM agent_link_endpoints UNION SELECT endpoint FROM agent_link_host WHERE endpoint<>''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var endpoint string
		if err := rows.Scan(&endpoint); err != nil {
			return nil, err
		}
		if !seen[endpoint] {
			seen[endpoint] = true
			result = append(result, linkhost.Address{Endpoint: endpoint, Kind: "configured"})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, b := linkhost.AddressPriority(result[i]), linkhost.AddressPriority(result[j])
		if a != b {
			return a < b
		}
		return result[i].Endpoint < result[j].Endpoint
	})
	return result, rows.Err()
}

func (s *Service) selectAgentAddress(ctx context.Context, endpoint string) (string, error) {
	addresses, err := s.agentAddresses(ctx)
	if err != nil {
		return "", err
	}
	for _, address := range addresses {
		if address.Endpoint != endpoint {
			continue
		}
		h := s.agentLinks
		h.setup.Lock()
		defer h.setup.Unlock()
		if err = s.enableAgentAddress(ctx, endpoint); err != nil {
			return "", err
		}
		return endpoint, nil
	}
	return "", agentlink.ErrInvalid
}

// Caller holds setup. Keep all previously selected listeners alive.
func (s *Service) enableAgentAddress(ctx context.Context, endpoint string) error {
	if _, err := peer.ValidateEndpoint(ctx, endpoint); err != nil {
		return agentlink.ErrInvalid
	}
	h := s.agentLinks
	var candidate *linkhost.PeerListener
	if err := s.probeAgentEndpoint(ctx, endpoint); err != nil {
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != "agent_link_unreachable" {
			return err
		}
		listen, err := linkhost.TCPListenAddress(ctx, endpoint)
		if err != nil || listen == "" {
			return protocol.APIError{Code: "agent_link_unreachable"}
		}
		candidate, err = linkhost.ListenPeer(s.ctx, listen, h.registry.Server())
		if err != nil {
			return protocol.APIError{Code: "agent_link_listener_unavailable"}
		}
		defer func() {
			if candidate != nil {
				candidate.Close()
			}
		}()
		if err = s.probeAgentEndpoint(ctx, endpoint); err != nil {
			return err
		}
	}
	if _, err := s.integrationDB.ExecContext(ctx, "INSERT OR IGNORE INTO agent_link_endpoints(endpoint) VALUES(?)", endpoint); err != nil {
		return err
	}
	if candidate != nil {
		if h.additional == nil {
			h.additional = map[string]*linkhost.PeerListener{}
		}
		h.additional[endpoint] = candidate
		candidate = nil
	}
	return nil
}

func (s *Service) restoreAgentAddresses(ctx context.Context) error {
	rows, err := s.integrationDB.QueryContext(ctx, "SELECT endpoint FROM agent_link_endpoints ORDER BY endpoint")
	if err != nil {
		return err
	}
	var endpoints []string
	for rows.Next() {
		var endpoint string
		if err = rows.Scan(&endpoint); err != nil {
			break
		}
		endpoints = append(endpoints, endpoint)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	h := s.agentLinks
	h.setup.Lock()
	defer h.setup.Unlock()
	for _, endpoint := range endpoints {
		probe, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		// A missing interface must not stop Pot or other saved addresses.
		_ = s.enableAgentAddress(probe, endpoint)
		cancel()
	}
	return nil
}
