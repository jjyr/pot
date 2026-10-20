package protocol

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
	"reflect"
	"slices"
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

func TestSendMessageToolAddressSchemaMatchesDecoder(t *testing.T) {
	type schema struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties bool                       `json:"additionalProperties"`
	}
	var addressSchema schema
	found := 0
	for _, raw := range core.ProductTools() {
		var tool struct {
			Name        string `json:"name"`
			InputSchema schema `json:"inputSchema"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatal(err)
		}
		if tool.Name != "send_message" {
			continue
		}
		found++
		if err := json.Unmarshal(tool.InputSchema.Properties["to"], &addressSchema); err != nil {
			t.Fatal(err)
		}
	}
	if found != 1 || addressSchema.Type != "object" || addressSchema.AdditionalProperties {
		t.Fatal("missing or open send_message address schema", found, addressSchema)
	}
	// Compare the published model schema to the real wire representation, rather
	// than duplicating its expected field names in another fixture.
	encoded, err := json.Marshal(AgentRef{NodeID: "logical-node", Kind: "assistant", AgentID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	keys := func(fields map[string]json.RawMessage) []string {
		out := make([]string, 0, len(fields))
		for key := range fields {
			out = append(out, key)
		}
		slices.Sort(out)
		return out
	}
	declared, actual := keys(addressSchema.Properties), keys(wire)
	required := slices.Clone(addressSchema.Required)
	slices.Sort(required)
	if !reflect.DeepEqual(declared, actual) || !reflect.DeepEqual(required, actual) {
		t.Fatalf("model address schema drifted from AgentRef: properties=%v required=%v wire=%v", declared, required, actual)
	}
	var kinds struct {
		Enum []string `json:"enum"`
	}
	if err = json.Unmarshal(addressSchema.Properties["kind"], &kinds); err != nil || len(kinds.Enum) == 0 {
		t.Fatal("address kind schema missing", err)
	}
	for _, kind := range kinds.Enum {
		t.Run(kind, func(t *testing.T) {
			// Build exactly the address a model sees in the schema and require the same
			// strict decoder used by send_message to accept all advertised destinations.
			address := map[string]any{}
			for key, raw := range addressSchema.Properties {
				var field struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(raw, &field); err != nil || field.Type != "string" {
					t.Fatal("address field is not a string", key, err)
				}
				address[key] = "sample-identity"
			}
			address["kind"] = kind
			arguments, err := json.Marshal(map[string]any{"to": address, "text": "message"})
			if err != nil {
				t.Fatal(err)
			}
			var request SendMail
			if err = DecodeStrict(arguments, &request); err != nil || !request.To.Valid() {
				t.Fatalf("advertised model address rejected by decoder: %s: %v", arguments, err)
			}
		})
	}
	for _, field := range required {
		omitted := map[string]json.RawMessage{}
		for key, value := range wire {
			if key != field {
				omitted[key] = value
			}
		}
		raw, _ := json.Marshal(omitted)
		var address AgentRef
		if err = DecodeStrict(raw, &address); err == nil {
			t.Fatal("published required field is optional in the decoder", field)
		}
	}
}
