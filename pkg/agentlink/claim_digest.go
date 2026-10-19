package agentlink

import (
	"encoding/json"
	"fmt"

	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
)

// claimDigest freezes only the old typed identity serialization for historical
// invitation receipts. Names, capability, request ID and host key remain exact;
// the protocol decoder itself never accepts old address or host fields.
func claimDigest(request pairRequest, remote peer.Identity, format int) (string, error) {
	var input any
	switch format {
	case 2:
		input = []any{request, remote}
	case 1:
		type ref struct {
			ControlID string `json:"control_id"`
			Kind      string `json:"kind"`
			AgentID   string `json:"agent_id"`
		}
		type identity struct {
			Address ref    `json:"address"`
			Name    string `json:"name"`
			Adapter string `json:"adapter"`
		}
		address := func(value protocol.AgentRef) ref { return ref{value.NodeID, value.Kind, value.AgentID} }
		legacy := func(value Identity) identity { return identity{address(value.Address), value.Name, value.Adapter} }
		pair := struct {
			ID         string   `json:"id"`
			Invitation string   `json:"invitation"`
			Local      identity `json:"local"`
			Peer       identity `json:"peer"`
			Code       string   `json:"code"`
		}{request.ID, request.Invitation, legacy(request.Local), legacy(request.Peer), request.Code}
		host := struct {
			ID        string `json:"id"`
			PublicKey string `json:"public_key"`
		}{remote.NodeID, remote.PublicKey}
		input = []any{pair, host}
	default:
		return "", fmt.Errorf("unknown Agent Link claim digest format")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	return digest(raw), nil
}
