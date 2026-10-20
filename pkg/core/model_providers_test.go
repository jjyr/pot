package core

import (
	"strings"
	"testing"
)

func TestModelProviderContractValidationAndBundleIsolation(t *testing.T) {
	valid := `{"schema_version":2,"id":"provider-test","version":"1","contributes":{"model_providers":{"providers":[{"id":"custom-api","name":"Custom API","protocol":"openai_responses","capabilities":{"catalog":"http"}}]}}}`
	var manifest PluginManifest
	if err := DecodeJSON([]byte(valid), &manifest); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if (PluginBundle{Manifest: manifest}).Validate() == nil {
		t.Fatal("Control contribution accepted as Runner bundle")
	}
	d := &manifest.Contributes.ModelProviders.Providers[0]
	d.Capabilities.Allowances = true
	if manifest.Validate() == nil {
		t.Fatal("executable capability accepted without adapter")
	}
	manifest.Contributes.ModelProviders.Adapter = &ModelProviderAdapter{Runtime: "node", Execution: "control", Entrypoint: "reader.mjs", Files: []string{"reader.mjs"}, CredentialEnv: "NODE_OPTIONS"}
	if manifest.Validate() == nil {
		t.Fatal("credential loader injection accepted")
	}
	manifest.Contributes.ModelProviders.Adapter.CredentialEnv = "API_KEY"
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	manifest.Contributes.ModelProviders.Adapter.Files = []string{"../reader.mjs"}
	if manifest.Validate() == nil {
		t.Fatal("unsafe adapter path accepted")
	}
}
func TestModelProviderSnapshotsPreserveUnknownAndRejectInvalidIdentity(t *testing.T) {
	var response ModelProviderResponse
	raw := `{"allowances":{"read_at":"2026-01-01T00:00:00Z","accounts":[{"identity_quality":"ambiguous","windows":[{"identity_quality":"ambiguous"}]}]}}`
	if err := DecodeJSON([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	if err := response.Validate("read_allowances"); err != nil {
		t.Fatal(err)
	}
	if response.Allowances.Accounts[0].Windows[0].Used != nil {
		t.Fatal("missing usage became zero")
	}
	response.Allowances.Accounts[0].IdentityQuality = "stable"
	if response.Validate("read_allowances") == nil {
		t.Fatal("stable unidentified account accepted")
	}
	response.Allowances.Accounts[0].ID = "account"
	response.Allowances.Accounts[0].Windows[0].Used = new(float64(-1))
	if response.Validate("read_allowances") == nil {
		t.Fatal("negative percentage accepted")
	}
	response.Allowances.Accounts[0].Windows[0].Used = new(float64(105))
	if err := response.Validate("read_allowances"); err != nil {
		t.Fatal("valid overage rejected", err)
	}
	if response.Validate("list_models") == nil {
		t.Fatal("allowance returned for catalog read")
	}
}
func TestProviderReaderRequestCannotChooseUndeclaredTypeProtocolOrMethod(t *testing.T) {
	p := PluginModelProviders{Providers: []ModelProviderDefinition{{ID: "test", Name: "Test", Protocol: "openai_responses", Capabilities: ModelProviderCapabilities{Catalog: "plugin"}}}, Adapter: &ModelProviderAdapter{Runtime: "node", Execution: "control", Entrypoint: "reader.mjs", Files: []string{"reader.mjs"}}}
	r := ModelProviderRequest{Version: 1, Method: "list_models", Connection: ModelProviderConnection{ID: "connection", Type: "test", Endpoint: "http://localhost:3425", Protocol: "openai_responses"}}
	if err := r.Validate(p); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ModelProviderRequest){func(r *ModelProviderRequest) { r.Connection.Type = "other" }, func(r *ModelProviderRequest) { r.Connection.Protocol = "gemini" }, func(r *ModelProviderRequest) { r.Method = "infer" }, func(r *ModelProviderRequest) { r.Connection.Endpoint = "http://user:pass@localhost" }} {
		changed := r
		mutate(&changed)
		if changed.Validate(p) == nil {
			t.Fatal("invalid request accepted", changed)
		}
	}
}

func TestProviderModelMetadataBoundsAndErrorCodes(t *testing.T) {
	catalog := ProviderModelCatalog{Models: []ProviderModel{{ID: "valid-model"}}, ReadAt: "2026-01-01T00:00:00Z"}
	response := ModelProviderResponse{Catalog: &catalog}
	for _, mutate := range []func(*ProviderModel){func(m *ProviderModel) { m.ID = "bad\nmodel" }, func(m *ProviderModel) { m.ID = string(make([]byte, 257)) }, func(m *ProviderModel) { m.ContextWindow = int64(MaxSafeInteger) + 1 }, func(m *ProviderModel) { m.MaxInput = int64(MaxSafeInteger) + 1 }, func(m *ProviderModel) { m.MaxOutput = int64(MaxSafeInteger) + 1 }} {
		model := ProviderModel{ID: "valid"}
		mutate(&model)
		catalog.Models = []ProviderModel{model}
		if response.Validate("list_models") == nil {
			t.Fatal("unrepresentable model accepted", model)
		}
	}
	for _, code := range []string{"authorization", "permission", "rate_limited", "unavailable", "overflow", "invalid_response"} {
		response := ModelProviderResponse{Error: &ModelProviderError{Code: code}}
		if err := response.Validate("read_allowances"); err != nil {
			t.Fatal(err)
		}
	}
	if (ModelProviderResponse{Error: &ModelProviderError{Code: "raw secret diagnostic"}}).Validate("list_models") == nil {
		t.Fatal("arbitrary plugin diagnostic accepted")
	}
	if (ModelProviderResponse{Error: &ModelProviderError{Code: "permission"}, Catalog: &catalog}).Validate("list_models") == nil {
		t.Fatal("error and catalog accepted together")
	}
}

func TestProviderPublicDefaultCredentialIsBoundedHeaderSafeData(t *testing.T) {
	p := PluginModelProviders{Providers: []ModelProviderDefinition{{ID: "test", Name: "Test", Protocol: "openai_responses", DefaultCredential: "public-app-id", Capabilities: ModelProviderCapabilities{Catalog: "http"}}}}
	if err := p.Validate(); err != nil {
		t.Fatal("public default rejected", err)
	}
	for _, credential := range []string{"public\r\nAuthorization: injected", "public\x00value", strings.Repeat("x", 4097)} {
		p.Providers[0].DefaultCredential = credential
		if p.Validate() == nil {
			t.Fatal("invalid public default accepted")
		}
	}
}

func TestProviderReaderRequestRejectsUndeclaredConfiguration(t *testing.T) {
	var request ModelProviderRequest
	if err := DecodeJSON([]byte(`{"version":1,"method":"list_models","connection":{"id":"connection","type":"test","revision":1,"endpoint":"http://localhost:3425","protocol":"openai_responses","config":{}}}`), &request); err == nil {
		t.Fatal("undeclared connection configuration accepted")
	}
}
