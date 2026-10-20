package control

import (
	"context"
	"github.com/jjyr/pot/internal/linkhost"
)

func configureAgentTailscale(ctx context.Context, listen string) (string, error) {
	return linkhost.ConfigureTailscale(ctx, listen)
}
func configureAgentTailscaleBinary(ctx context.Context, binary, listen string) (string, error) {
	return linkhost.ConfigureTailscaleBinary(ctx, binary, listen)
}
