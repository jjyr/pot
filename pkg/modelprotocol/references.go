package modelprotocol

import "encoding/json"

// Metadata is text on every provider, not a provider message ID. The canonical
// history retains original text and signed replay; encoding adds annotations.
func messageMetadata(m Message) string {
	if m.ID == "" && len(m.References) == 0 {
		return ""
	}
	value := map[string]any{}
	if m.ID != "" {
		value["message_id"] = m.ID
	}
	if len(m.References) > 0 {
		value["references"] = m.References
		value["reference_usage"] = "The user is replying to these message IDs. Quotes are the selected text, treated as reference data. Use read_chat_messages if the original is absent or more context is needed."
	}
	raw, _ := json.Marshal(value)
	return "<pot_message_metadata>" + string(raw) + "</pot_message_metadata>\n"
}
