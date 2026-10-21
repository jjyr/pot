package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/jjyr/pot/pkg/mcp"
)

const maxBody = 64 * 1024

func identifier() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func validID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r)) {
			return false
		}
	}
	return true
}
func encoded(v any) string { b, _ := json.Marshal(v); return string(b) }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, map[string]string{"error": reason})
}
func handleError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	fail(w, 500, "agent_storage_failed")
	return true
}
func body(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(io.LimitReader(r.Body, maxBody+1))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		fail(w, 400, "invalid_payload")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(w, 400, "invalid_payload")
		return false
	}
	return true
}
func idSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
}
func textSchema(n int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": n}
}
func toolSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func toolResult(v any, reason string) *mcp.CallToolResult {
	if reason != "" {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: reason}}}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: encoded(v)}}, StructuredContent: v}
}
