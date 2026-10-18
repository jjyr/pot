package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ShellSettings struct {
	Executable           string `json:"executable"`
	AllowLoginShell      bool   `json:"allow_login_shell"`
	ShellSnapshot        bool   `json:"shell_snapshot"`
	SessionIdleTimeoutMS uint64 `json:"session_idle_timeout_ms"`
	DefaultOutputBytes   uint64 `json:"default_output_bytes"`
	MaxOutputBytes       uint64 `json:"max_output_bytes"`
	MaxSessions          uint64 `json:"max_sessions"`
	DefaultYieldMS       uint64 `json:"default_yield_ms"`
	MaxYieldMS           uint64 `json:"max_yield_ms"`
}

func DefaultShellSettings() ShellSettings {
	return ShellSettings{"/bin/bash", true, true, 1800000, 16384, 65536, 8, 10000, 30000}
}
func (s *ShellSettings) UnmarshalJSON(data []byte) error {
	type plain ShellSettings
	value := plain(DefaultShellSettings())
	if err := DecodeJSON(data, &value); err != nil {
		return err
	}
	*s = ShellSettings(value)
	return nil
}
func (s ShellSettings) Validate() error {
	if s.Executable == "" || s.DefaultOutputBytes == 0 || s.DefaultOutputBytes > s.MaxOutputBytes || s.MaxOutputBytes > MaxSafeInteger || s.MaxSessions == 0 || s.SessionIdleTimeoutMS == 0 || s.DefaultYieldMS == 0 || s.DefaultYieldMS > s.MaxYieldMS {
		return fmt.Errorf("invalid shell limits")
	}
	return nil
}

type AgentSettings struct {
	ModelCatalog     *ModelCatalog           `json:"model_catalog,omitempty"`
	Plugins          map[string]PluginConfig `json:"plugins"`
	ContextWindow    uint64                  `json:"context_window"`
	MaxInputTokens   *uint64                 `json:"max_input_tokens"`
	CompactAt        *uint64                 `json:"compact_at"`
	AutoCompact      bool                    `json:"auto_compact"`
	UseProviderUsage bool                    `json:"use_provider_usage"`
	ContextReserve   uint64                  `json:"context_reserve"`
	SystemPrompt     *string                 `json:"system_prompt"`
	BuiltinTools     []string                `json:"builtin_tools"`
	AllowedTools     *[]string               `json:"allowed_tools"`
	RequestTimeoutMS uint64                  `json:"request_timeout_ms"`
	Shell            ShellSettings           `json:"shell"`
}

func DefaultAgentSettings() AgentSettings {
	return AgentSettings{Plugins: map[string]PluginConfig{}, ContextWindow: 32768, AutoCompact: true, UseProviderUsage: true, ContextReserve: 4096, BuiltinTools: []string{"exec_command", "write_stdin", "apply_patch", "exec", "wait"}, RequestTimeoutMS: 180000, Shell: DefaultShellSettings()}
}
func (s *AgentSettings) UnmarshalJSON(data []byte) error {
	type plain AgentSettings
	value := plain(DefaultAgentSettings())
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, present := fields["plugins"]; present {
		value.Plugins = map[string]PluginConfig{}
	}
	if err := DecodeJSON(data, &value); err != nil {
		return err
	}
	*s = AgentSettings(value)
	return nil
}
func (s AgentSettings) Validate() error {
	if s.ContextWindow == 0 || s.ContextWindow > MaxSafeInteger || s.ContextReserve >= s.ContextWindow || s.MaxInputTokens != nil && (*s.MaxInputTokens == 0 || *s.MaxInputTokens > MaxSafeInteger) || s.CompactAt != nil && (*s.CompactAt == 0 || *s.CompactAt >= s.ContextWindow) || s.RequestTimeoutMS == 0 || s.RequestTimeoutMS > MaxSafeInteger {
		return fmt.Errorf("invalid agent context/request limits")
	}
	names := map[string]bool{}
	for _, name := range s.BuiltinTools {
		switch name {
		case "exec_command", "write_stdin", "apply_patch", "exec", "wait":
		default:
			return fmt.Errorf("unknown built-in tool %s", name)
		}
		if names[name] {
			return fmt.Errorf("duplicate built-in tool %s", name)
		}
		names[name] = true
	}
	if s.ModelCatalog != nil {
		if err := s.ModelCatalog.Validate(); err != nil {
			return err
		}
	}
	for name, p := range s.Plugins {
		if p.Bundle != nil {
			if p.Bundle.Manifest.ID != name {
				return fmt.Errorf("plugin identity mismatch")
			}
			if err := p.Bundle.Validate(); err != nil {
				return err
			}
		}
	}
	return s.Shell.Validate()
}

type Parameters struct {
	MaxOutputTokens uint64        `json:"max_output_tokens" required:"true"`
	ReasoningEffort *string       `json:"reasoning_effort"`
	Agent           AgentSettings `json:"agent" required:"true"`
}
type ToolPolicy struct {
	WorkerOrchestration bool   `json:"worker_orchestration"`
	SummaryReadOnly     bool   `json:"summary_read_only"`
	Unattended          string `json:"unattended" required:"true"`
}
type ModelSettings struct {
	CatalogProvider *string             `json:"catalog_provider"`
	ContextWindow   *uint64             `json:"context_window"`
	MaxOutputTokens *uint64             `json:"max_output_tokens"`
	InputModalities map[string][]string `json:"input_modalities,omitempty"`
}
type Profile struct {
	ID            string         `json:"id" required:"true"`
	Revision      uint64         `json:"revision" required:"true"`
	Name          string         `json:"name" required:"true"`
	Provider      string         `json:"provider" required:"true"`
	Model         string         `json:"model" required:"true"`
	AuthRef       string         `json:"auth_ref" required:"true"`
	ModelSettings *ModelSettings `json:"model_settings,omitempty"`
	Params        Parameters     `json:"params" required:"true"`
	ToolPolicy    ToolPolicy     `json:"tool_policy" required:"true"`
}

func (p Profile) Validate() error {
	if p.ModelSettings != nil {
		if len(p.ModelSettings.InputModalities) > 10000 {
			return fmt.Errorf("too many model input descriptors")
		}
		for model, modalities := range p.ModelSettings.InputModalities {
			if strings.TrimSpace(model) == "" || len(model) > 256 {
				return fmt.Errorf("invalid model input identity")
			}
			d := DefaultModelDescriptor()
			d.InputModalities = modalities
			if err := d.Validate(); err != nil {
				return err
			}
		}
	}
	if err := p.Params.Agent.Validate(); err != nil {
		return err
	}
	if !Identifier(p.ID) || p.Revision == 0 || p.Revision > MaxSafeInteger || p.Name == "" || len(p.Name) > 256 || p.Model == "" || len(p.Model) > 256 || !Identifier(p.Provider) || p.AuthRef != "provider:"+p.Provider || p.Params.MaxOutputTokens == 0 || p.Params.MaxOutputTokens > MaxSafeInteger || p.ToolPolicy.WorkerOrchestration || !p.ToolPolicy.SummaryReadOnly || p.ToolPolicy.Unattended != "runtime" {
		return fmt.Errorf("invalid profile or credential/tool policy")
	}
	if p.Params.Agent.ModelCatalog != nil {
		d, err := p.Params.Agent.ModelCatalog.Resolve(p.Provider, p.Model)
		if err != nil {
			return err
		}
		if !d.SupportedInAPI {
			return fmt.Errorf("model does not support API execution")
		}
	}
	return nil
}

type ResolvedSpec struct {
	Provider              string     `json:"provider" required:"true"`
	Model                 string     `json:"model" required:"true"`
	AuthRef               string     `json:"auth_ref" required:"true"`
	Params                Parameters `json:"params" required:"true"`
	ToolPolicy            ToolPolicy `json:"tool_policy" required:"true"`
	GatewayProtocol       string     `json:"gateway_protocol" required:"true"`
	ContextWindow         uint64     `json:"context_window" required:"true"`
	SourceProfileID       string     `json:"source_profile_id" required:"true"`
	SourceProfileRevision uint64     `json:"source_profile_revision" required:"true"`
	SpecHash              string     `json:"spec_hash" required:"true"`
}

func (p Profile) Resolve() (ResolvedSpec, error) {
	if err := p.Validate(); err != nil {
		return ResolvedSpec{}, err
	}
	data, err := json.Marshal(p.Params)
	if err != nil {
		return ResolvedSpec{}, err
	}
	var params Parameters
	if err = DecodeJSON(data, &params); err != nil {
		return ResolvedSpec{}, err
	}
	for name, plugin := range params.Agent.Plugins {
		if plugin.Enabled && plugin.Bundle == nil {
			return ResolvedSpec{}, fmt.Errorf("plugin %s must be pinned before resolution", name)
		}
		plugin.Source = nil
		params.Agent.Plugins[name] = plugin
	}
	spec := ResolvedSpec{p.Provider, p.Model, p.AuthRef, params, p.ToolPolicy, "openai_chat", params.Agent.ContextWindow, p.ID, p.Revision, ""}
	err = spec.Rehash()
	return spec, err
}
func (s *ResolvedSpec) Rehash() error {
	s.SpecHash = ""
	hash, err := HashJSON(s)
	if err == nil {
		s.SpecHash = hash
	}
	return err
}
func (s ResolvedSpec) Validate() error {
	original := s.SpecHash
	if err := s.Rehash(); err != nil {
		return err
	}
	if s.SpecHash != original {
		return fmt.Errorf("resolved spec checksum mismatch")
	}
	if s.ContextWindow != s.Params.Agent.ContextWindow {
		return fmt.Errorf("conflicting resolved context window")
	}
	p := Profile{ID: s.SourceProfileID, Revision: s.SourceProfileRevision, Name: "resolved", Provider: s.Provider, Model: s.Model, AuthRef: s.AuthRef, Params: s.Params, ToolPolicy: s.ToolPolicy}
	if err := p.Validate(); err != nil {
		return err
	}
	switch s.GatewayProtocol {
	case "openai_chat", "openai_responses", "anthropic_messages", "gemini", "unknown":
	default:
		return fmt.Errorf("unsupported gateway protocol")
	}
	for _, p := range s.Params.Agent.Plugins {
		if p.Source != nil || p.Enabled && p.Bundle == nil {
			return fmt.Errorf("unpinned plugin in resolved configuration")
		}
	}
	return nil
}

type Thread struct {
	Owner      string       `json:"owner"`
	Generation uint64       `json:"generation"`
	Config     ResolvedSpec `json:"config"`
}
