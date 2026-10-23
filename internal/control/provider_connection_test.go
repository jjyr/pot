package control

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderDiscoveryAndInference(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ protocol, path, header, prefix, events string }{
		{"openai_chat", "/v1/chat/completions", "Authorization", "Bearer ", `{"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}`},
		{"openai_responses", "/v1/responses", "Authorization", "Bearer ", `{"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}}`},
		{"anthropic_messages", "/v1/messages", "x-api-key", "", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"OK"}}` + "\n\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}` + "\n\ndata: " + `{"type":"message_stop"}`},
		{"gemini", "/v1/models/model:streamGenerateContent", "x-goog-api-key", "", `{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`},
	} {
		t.Run(tt.protocol, func(t *testing.T) {
			s, _ := configurationService(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(tt.header) != tt.prefix+"private-test-key" {
					t.Error("credential missing")
					w.WriteHeader(401)
					return
				}
				if r.Method == "GET" {
					if r.URL.Path != "/v1/models" {
						t.Error(r.URL.Path)
					}
					fmt.Fprint(w, `{"data":[{"id":"model"}],"models":[{"name":"models/model"}]}`)
					return
				}
				calls++
				if r.URL.Path != tt.path {
					t.Errorf("path %s", r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if tt.protocol != "gemini" && (body["model"] != "model" || body["stream"] != true) {
					t.Error(body)
				}
				if tt.protocol == "gemini" && r.URL.Query().Get("alt") != "sse" {
					t.Error("missing SSE")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", tt.events)
			}))
			defer server.Close()
			input := CheckProvider{Endpoint: server.URL + "/v1", Protocol: tt.protocol, APIKey: "private-test-key"}
			result, err := s.Profiles.Check(context.Background(), input)
			if err != nil || !result.Supported || len(result.Catalog.Models) != 1 || calls != 0 {
				t.Fatal(result, err, calls)
			}
			if _, err = s.Profiles.Test(context.Background(), TestProvider{Endpoint: input.Endpoint, Protocol: input.Protocol, APIKey: input.APIKey, ProfileID: input.ProfileID, Model: "model"}); err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
			profile, err := s.Profiles.Save(context.Background(), SaveProfile{RequestID: "save-provider", Name: "Provider", Model: "model", Endpoint: input.Endpoint, Protocol: tt.protocol, APIKey: input.APIKey})
			if err != nil {
				t.Fatal(err)
			}
			route, err := s.Profiles.registry.Get(context.Background(), profile.Provider)
			if err != nil || !strings.Contains(route.Endpoint, tt.path) && tt.protocol != "gemini" {
				t.Fatal(route, err)
			}
			input.APIKey = ""
			input.ProfileID = profile.ID
			if _, err = s.Profiles.Test(context.Background(), TestProvider{Endpoint: input.Endpoint, Protocol: input.Protocol, APIKey: input.APIKey, ProfileID: input.ProfileID, Model: "model"}); err != nil || calls != 2 {
				t.Fatal(err, calls)
			}
			input.Endpoint = server.URL + "/other/v1"
			if _, err = s.Profiles.Test(context.Background(), TestProvider{Endpoint: input.Endpoint, Protocol: input.Protocol, APIKey: input.APIKey, ProfileID: input.ProfileID, Model: "model"}); err == nil || calls != 2 {
				t.Fatal("saved credential sent to changed route", err, calls)
			}
		})
	}
}

func TestDiscoveredInputModalitiesSurviveSelectionAndReload(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected inference request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"custom/vision","modalities":{"input":["text","image","video"]}},{"id":"custom/pdf","modalities":{"input":["text","pdf"]}},{"id":"custom/unknown"}]}`)
	}))
	defer server.Close()
	ctx := context.Background()
	check, err := s.Profiles.Check(ctx, CheckProvider{Endpoint: server.URL + "/v1", Protocol: "openai_responses", APIKey: "test-key"})
	if err != nil || check.Catalog == nil {
		t.Fatal(check, err)
	}
	ids := []string{}
	inputs := map[string][]string{}
	for _, model := range check.Catalog.Models {
		ids = append(ids, model.ID)
		if len(model.InputModalities) > 0 {
			inputs[model.ID] = conversationModalities(model.InputModalities)
		}
	}
	if len(inputs) != 2 {
		t.Fatal(inputs)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "save-inputs", Name: "Custom", Model: "custom/vision", Endpoint: server.URL + "/v1", Protocol: "openai_responses", APIKey: "test-key", Models: ids, ModelSettings: &core.ModelSettings{InputModalities: inputs}})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenProfiles(s.Profiles.path, s.Profiles.pluginRoot, s.Profiles.registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		model string
		want  core.AttachmentCapabilities
	}{
		{"custom/vision", core.AttachmentCapabilities{Image: true}},
		{"custom/pdf", core.AttachmentCapabilities{PDF: true}},
		{"custom/unknown", core.AttachmentCapabilities{}},
	} {
		spec, err := reopened.ResolveAssistant(AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: tt.model})
		if err != nil || spec.AttachmentCapabilities() != tt.want {
			t.Fatalf("%s: caps=%+v err=%v", tt.model, spec.AttachmentCapabilities(), err)
		}
	}
}

func TestModelListOptionalModalitiesCompatibility(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	for _, extension := range []string{"", `,"modalities":null`, `,"modalities":["text","image"]`, `,"modalities":"image"`, `,"modalities":{"input":true}`, `,"modalities":{"input":["text",42]}`, `,"modalities":{"output":["image"]}`, `,"modalities":{"input":[]}`} {
		t.Run(extension, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"object":"list","data":[{"id":"standard","object":"model","created":123,"owned_by":"openai"%s},{"id":"extended","modalities":{"input":["text","image"]}}]}`, extension)
			}))
			defer server.Close()
			result, err := s.Profiles.Check(context.Background(), CheckProvider{Endpoint: server.URL + "/v1", Protocol: "openai_chat", APIKey: "test-key"})
			if err != nil || !result.Supported || result.Catalog == nil || len(result.Catalog.Models) != 2 || result.Catalog.Models[0].ID != "extended" || len(result.Catalog.Models[0].InputModalities) != 2 || len(result.Catalog.Models[1].InputModalities) != 0 {
				t.Fatalf("valid model list rejected or absent facts treated as unsupported: %+v, %v", result, err)
			}
		})
	}
}

func TestProviderTestRejectsHTTPAndStreamFailures(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	for _, tt := range []struct {
		name              string
		status            int
		contentType, body string
	}{
		{"unauthorized", 401, "text/plain", "private-test-key"},
		{"missing", 404, "text/plain", "private-test-key"},
		{"html", 200, "text/html", "<html>OK</html>"},
		{"empty", 200, "text/event-stream", ""},
		{"incomplete", 200, "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n"},
		{"error", 200, "text/event-stream", "data: {\"error\":{\"message\":\"private-test-key\"}}\n\n"},
		{"oversize", 200, "text/event-stream", strings.Repeat("a", 262145)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			_, err := s.Profiles.Test(context.Background(), TestProvider{Endpoint: server.URL + "/v1", Protocol: "openai_chat", APIKey: "private-test-key", Model: "model"})
			if err == nil || strings.Contains(err.Error(), "private-test-key") {
				t.Fatal(err)
			}
		})
	}
	if _, err := s.Profiles.Save(context.Background(), SaveProfile{RequestID: "conflict", Name: "Provider", Model: "model", Endpoint: "https://example.com/v1/responses", Protocol: "openai_chat", APIKey: "key"}); err == nil {
		t.Fatal("saved conflicting protocol")
	}
}

func TestConnectionRequestDecodesThroughPublicAPI(t *testing.T) {
	t.Parallel()
	var request TestProvider
	if err := core.DecodeJSON([]byte(`{"endpoint":"https://example.com/v1","protocol":"openai_chat","api_key":"test-key","profile_id":"","model":"model","reasoning_effort":null}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Endpoint != "https://example.com/v1" || request.Model != "model" {
		t.Fatal(request)
	}
}
