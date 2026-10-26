package transport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/internal/resources"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestExternalMessageFileCompletionToHTTPPreviewAndDownload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.go")
	original := []byte("package synthetic\n// delivered outside the execution workspace\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	// The fixture runs a real assistant turn against a synthetic model server,
	// waits for asynchronous projection, then returns the immutable resource.
	app, items := resourceApplicationSources(t, []string{path + ":2"})
	owner := localAssistantID(t, app.Control)
	item := items[0]
	if item.Entry != "source.go" {
		t.Fatal("external path leaked into resource API entry", item)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	handler := Handler(app, "operator")
	get := func(path, assistant, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("GET", path+"?assistant_id="+assistant, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	prefix := "/api/v1/resources/" + item.ID
	for _, endpoint := range []string{"/content", "/download"} {
		response := get(prefix+endpoint, owner, "operator")
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), original) {
			t.Fatal("immutable original unavailable", endpoint, response.Code)
		}
	}
	response := get(prefix+"/chunk", owner, "operator")
	var chunk protocol.FileChunk
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &chunk) != nil {
		t.Fatal("preview unavailable", response.Code)
	}
	data, err := base64.StdEncoding.DecodeString(chunk.Data)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("preview changed bytes", err)
	}
	c, err := app.Control.Conversation(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range snapshot.Messages {
		if message.Role != protocol.Assistant {
			continue
		}
		response := get("/api/v1/message-files/"+message.ID, owner, "operator")
		var refs []resources.MessageFile
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &refs) != nil || len(refs) != 1 || refs[0].State != "ready" || refs[0].Href != path+":2" {
			t.Fatal("HTTP file status lost the completed source reference", response.Code, response.Body.String())
		}
	}
	if get(prefix+"/download", "other-owner", "operator").Code < 400 || get(prefix+"/download", owner, "wrong-token").Code < 400 {
		t.Fatal("broader source reads weakened resource API authorization")
	}
}
