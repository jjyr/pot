package spaces

import (
	"context"
	"encoding/json"

	"github.com/jjyr/pot/pkg/protocol"
)

type mailEndpoint struct {
	manager *Manager
	id      string
}

func (e mailEndpoint) NodeID() string { return e.id }
func (e mailEndpoint) call(ctx context.Context, request protocol.RelayRequest, result any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	reply, err := e.manager.Call(ctx, e.id, "POST", protocol.AssistantMailAPI, body)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return protocol.DecodeStrict(reply, result)
}
func (e mailEndpoint) Directory(ctx context.Context) (protocol.Directory, error) {
	var value protocol.Directory
	err := e.call(ctx, protocol.RelayRequest{Operation: "directory"}, &value)
	return value, err
}
func (e mailEndpoint) Register(ctx context.Context, directory protocol.Directory) error {
	return e.call(ctx, protocol.RelayRequest{Operation: "register", Directory: &directory}, nil)
}
func (e mailEndpoint) Pending(ctx context.Context, target string) ([]protocol.Envelope, error) {
	var value []protocol.Envelope
	err := e.call(ctx, protocol.RelayRequest{Operation: "pending", TargetNodeID: target}, &value)
	return value, err
}
func (e mailEndpoint) Deliver(ctx context.Context, message protocol.Envelope) (protocol.Receipt, error) {
	var value protocol.Receipt
	err := e.call(ctx, protocol.RelayRequest{Operation: "deliver", Message: &message}, &value)
	return value, err
}
func (e mailEndpoint) Acknowledge(ctx context.Context, receipt protocol.Receipt) error {
	return e.call(ctx, protocol.RelayRequest{Operation: "acknowledge", Receipt: &receipt}, nil)
}
