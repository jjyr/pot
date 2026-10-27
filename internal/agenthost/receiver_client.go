package agenthost

import (
	"context"
	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
)

// Keep native callers behind their existing small adapter. The SDK transport,
// resource-bound bearer and receiver protocol calls are shared with Pot.
type receiverClient struct{ *mcpreceiver.Client }

func connectReceiver(ctx context.Context, endpoint, token string, identity agentlink.MCPReceiver) (*receiverClient, error) {
	client, e := mcpreceiver.Connect(ctx, endpoint, token, identity)
	if e != nil {
		return nil, e
	}
	return &receiverClient{client}, nil
}
func (c *receiverClient) close() { c.Close() }
func (c *receiverClient) call(ctx context.Context, name string, input, output any) error {
	return c.Call(ctx, name, input, output)
}
