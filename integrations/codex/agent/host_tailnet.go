package agent

import (
	"context"
	"github.com/jjyr/pot/internal/linkhost"
)

// Only an explicit local Configure action changes Tailscale Serve. The shared
// helper reuses this peer listener's mapping and never publishes owner APIs.
func configureHostTailscale(ctx context.Context) (string, error) {
	return linkhost.ConfigureTailscale(ctx, hostPeerListen)
}
func configureHostTailscaleBinary(ctx context.Context, binary string) (string, error) {
	return linkhost.ConfigureTailscaleBinary(ctx, binary, hostPeerListen)
}
