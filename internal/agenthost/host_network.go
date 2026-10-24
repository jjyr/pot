package agenthost

import (
	"context"
	"errors"
	"time"

	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/peer"
)

// Peer listeners expose only authenticated host identity and message transport.
// Owner configuration remains on the separate fixed loopback port.
func (h *agentHost) peerServer() *peer.Server { return h.peerRegistry.Server() }
func (h *agentHost) startPeerNetwork(ctx context.Context) error {
	if err := h.startDefaultPeerNetwork(ctx); err != nil {
		return err
	}
	return h.restoreAddresses(ctx)
}
func (h *agentHost) startDefaultPeerNetwork(ctx context.Context) error {
	h.accessMu.Lock()
	defer h.accessMu.Unlock()
	h.peerCtx = ctx
	peer, err := linkhost.ListenPeer(h.peerCtx, h.runtime().PeerListen, h.peerServer())
	if err != nil {
		return err
	}
	h.loopbackPeer = peer
	var endpoint string
	if err = h.db.QueryRowContext(ctx, "SELECT endpoint FROM agent_adapter_host WHERE singleton=1").Scan(&endpoint); err != nil {
		return err
	}
	if endpoint != "" {
		startup, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		candidate, restoreErr := h.preparePeer(startup, endpoint)
		cancel()
		if restoreErr != nil {
			h.networkError = agentlink.ErrUnavailable.Error()
		} else {
			h.directPeer = candidate
			h.networkError = ""
		}
	}
	return nil
}

// A candidate is made usable and checked before replacing durable configuration.
// Failed probes leave the prior listener, address and connections intact.
func (h *agentHost) configureEndpoint(ctx context.Context, raw string) error {
	endpoint, err := peer.ValidateEndpoint(ctx, raw)
	if err != nil {
		return err
	}
	h.accessMu.Lock()
	defer h.accessMu.Unlock()
	candidate, err := h.preparePeer(ctx, endpoint)
	if err != nil {
		return err
	}
	if _, err = h.db.ExecContext(ctx, "UPDATE agent_adapter_host SET endpoint=? WHERE singleton=1", endpoint); err != nil {
		if candidate != nil {
			candidate.Close()
		}
		return err
	}
	if candidate != nil {
		old := h.directPeer
		h.directPeer = candidate
		if old != nil {
			old.Close()
		}
	}
	h.networkError = ""
	return nil
}
func (h *agentHost) preparePeer(ctx context.Context, endpoint string) (*linkhost.PeerListener, error) {
	if _, err := peer.ValidateEndpoint(ctx, endpoint); err != nil {
		return nil, err
	}
	// Tailscale Serve and an existing TCP forwarder already reach the loopback
	// peer listener. Do not bind their incoming port a second time.
	probeErr := h.probeEndpoint(ctx, endpoint)
	if probeErr == nil {
		return nil, nil
	}
	if errors.Is(probeErr, agentlink.ErrConflict) {
		return nil, probeErr
	}
	listen, err := linkhost.TCPListenAddress(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if listen == "" {
		return nil, probeErr
	}
	if listen == h.runtime().Listen {
		return nil, agentlink.ErrInvalid
	}
	var candidate *linkhost.PeerListener
	if !(h.loopbackPeer != nil && listen == h.loopbackPeer.Addr() || h.directPeer != nil && listen == h.directPeer.Addr()) {
		candidate, err = linkhost.ListenPeer(h.peerCtx, listen, h.peerServer())
		if err != nil {
			return nil, agentlink.ErrUnavailable
		}
	}
	if err = h.probeEndpoint(ctx, endpoint); err != nil {
		if candidate != nil {
			candidate.Close()
		}
		return nil, err
	}
	return candidate, nil
}
