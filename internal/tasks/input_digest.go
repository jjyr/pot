package tasks

import (
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

const currentInputDigestFormat = 2

type legacyInputAgentRef struct {
	ControlID string `json:"control_id"`
	Kind      string `json:"kind"`
	AgentID   string `json:"agent_id"`
}

func legacyInputRef(ref protocol.AgentRef) legacyInputAgentRef {
	return legacyInputAgentRef{ref.NodeID, ref.Kind, ref.AgentID}
}

// inputDigest is a versioned content binding, independent of the wire decoder.
// Format 1 remains exact because a historical parent need not be stored here.
// Its migration receipt never permits accepting the old communication protocol.
func inputDigest(format int, text, kind string, sender protocol.AgentRef, parent *protocol.Envelope) (string, error) {
	if format == currentInputDigestFormat {
		return core.HashJSON([]any{text, kind, sender, parent})
	}
	if format != 1 {
		return "", fmt.Errorf("unknown Task input digest format")
	}
	var legacyParent any
	if parent != nil {
		legacyParent = struct {
			protocol.Envelope
			From legacyInputAgentRef `json:"from"`
			To   legacyInputAgentRef `json:"to"`
		}{*parent, legacyInputRef(parent.From), legacyInputRef(parent.To)}
	}
	return core.HashJSON([]any{text, kind, legacyInputRef(sender), legacyParent})
}
