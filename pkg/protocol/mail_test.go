package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageAddressesUseNodeIdentityOnly(t *testing.T) {
	ref := AgentRef{NodeID: "logical-node", Kind: "assistant", AgentID: "main"}
	raw, err := json.Marshal(ref)
	if err != nil || string(raw) != `{"node_id":"logical-node","kind":"assistant","agent_id":"main"}` || !ref.Valid() {
		t.Fatal(string(raw), err)
	}
	var parsed AgentRef
	if err = DecodeStrict([]byte(`{"control_id":"logical-node","kind":"assistant","agent_id":"main"}`), &parsed); err == nil {
		t.Fatal("old communication address accepted")
	}
	raw, err = json.Marshal(Directory{NodeID: "logical-node", Assistants: []MailAssistant{{Address: ref, Name: "Main"}}})
	if err != nil || strings.Contains(string(raw), `"control_id"`) || !strings.Contains(string(raw), `"node_id":"logical-node"`) {
		t.Fatal(string(raw), err)
	}
	var directory Directory
	if err = DecodeStrict([]byte(`{"control_id":"old-node","assistants":[],"tasks":[]}`), &directory); err == nil {
		t.Fatal("old directory identity accepted")
	}
}
