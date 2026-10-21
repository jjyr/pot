package agent

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/peer"
)

type hostAddress = linkhost.Address

func (h *agentHost) addresses(ctx context.Context) []hostAddress {
	h.discoveryMu.Lock()
	defer h.discoveryMu.Unlock()
	if h.discover != nil {
		return h.discover(ctx)
	}
	if time.Since(h.discoveryAt) > 10*time.Second {
		h.discovered = linkhost.DiscoverAddresses(ctx, hostPeerListen)
		h.discoveryAt = time.Now()
	}
	result := append([]hostAddress{}, h.discovered...)
	seen := map[string]bool{}
	for _, a := range result {
		seen[a.Endpoint] = true
	}
	rows, err := h.db.QueryContext(ctx, "SELECT endpoint FROM codex_agent_endpoints UNION SELECT endpoint FROM codex_agent_host WHERE endpoint<>''")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var endpoint string
			if rows.Scan(&endpoint) == nil && !seen[endpoint] {
				seen[endpoint] = true
				result = append(result, hostAddress{Endpoint: endpoint, Kind: "configured"})
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if left, right := linkhost.AddressPriority(result[i]), linkhost.AddressPriority(result[j]); left != right {
			return left < right
		}
		return result[i].Endpoint < result[j].Endpoint
	})
	return result
}

func (h *agentHost) selectAddress(ctx context.Context, endpoint string) (string, error) {
	var selected *hostAddress
	for _, address := range h.addresses(ctx) {
		if address.Endpoint == endpoint {
			selected = &address
			break
		}
	}
	if selected == nil {
		return "", agentlink.ErrInvalid
	}
	if selected.RequiresSetup {
		actual, err := configureHostTailscale(ctx)
		if err != nil {
			return "", err
		}
		h.discoveryMu.Lock()
		h.discoveryAt = time.Time{}
		h.discoveryMu.Unlock()
		if actual != endpoint {
			return "", agentlink.ErrConflict
		}
	}
	if err := h.enableAddress(ctx, endpoint); err != nil {
		return "", err
	}
	return endpoint, nil
}

func (h *agentHost) enableAddress(ctx context.Context, endpoint string) error {
	// Persist only after the selected listener authenticates as this exact host.
	if _, err := peer.ValidateEndpoint(ctx, endpoint); err != nil {
		return agentlink.ErrInvalid
	}
	h.accessMu.Lock()
	defer h.accessMu.Unlock()
	candidate, err := h.preparePeer(ctx, endpoint)
	if err != nil {
		return err
	}
	if _, err = h.db.ExecContext(ctx, "INSERT OR IGNORE INTO codex_agent_endpoints(endpoint) VALUES(?)", endpoint); err != nil {
		if candidate != nil {
			candidate.Close()
		}
		return err
	}
	if candidate != nil {
		if h.additionalPeers == nil {
			h.additionalPeers = make(map[string]*linkhost.PeerListener)
		}
		h.additionalPeers[endpoint] = candidate
	}
	return nil
}

func (h *agentHost) restoreAddresses(ctx context.Context) error {
	rows, err := h.db.QueryContext(ctx, "SELECT endpoint FROM codex_agent_endpoints ORDER BY endpoint")
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
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		probe, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		// An unavailable interface must not stop the local management page or other addresses.
		_ = h.enableAddress(probe, endpoint)
		cancel()
	}
	return nil
}
