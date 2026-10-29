package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
	webassets "github.com/jjyr/pot/web"
)

// AssistantSelection is editable intent belonging to one assistant. It never
// mutates a Provider profile or a persisted Task/running-turn specification.
type AssistantSelection struct {
	ProfileID       string              `json:"profile_id" required:"true"`
	ProfileRevision uint64              `json:"profile_revision" required:"true"`
	Model           string              `json:"model" required:"true"`
	ContextWindow   *uint64             `json:"context_window"`
	MaxOutputTokens *uint64             `json:"max_output_tokens"`
	ReasoningEffort *string             `json:"reasoning_effort"`
	TaskDefault     *TaskModelSelection `json:"task_default,omitempty"`
}
type TaskModelSelection struct {
	ContextWindow   *uint64 `json:"context_window"`
	MaxOutputTokens *uint64 `json:"max_output_tokens"`
	ReasoningEffort *string `json:"reasoning_effort"`
	ProfileID       string  `json:"profile_id" required:"true"`
	Model           string  `json:"model" required:"true"`
}
type SaveAssistantConfiguration struct {
	RequestID          string             `json:"request_id" required:"true"`
	ExpectedGeneration uint64             `json:"expected_generation" required:"true"`
	ExpectedRevision   uint64             `json:"expected_revision" required:"true"`
	Selection          AssistantSelection `json:"selection" required:"true"`
}
type AssistantConfiguration struct {
	Owner      string             `json:"owner"`
	Generation uint64             `json:"generation"`
	Config     *core.ResolvedSpec `json:"config"`
	Revision   uint64             `json:"revision"`
	Selection  AssistantSelection `json:"selection"`
	Pending    bool               `json:"pending"`
}

func normalizedSelection(value AssistantSelection) AssistantSelection {
	value.Model = strings.TrimSpace(value.Model)
	if value.TaskDefault != nil {
		taskDefault := *value.TaskDefault
		taskDefault.ProfileID = strings.TrimSpace(taskDefault.ProfileID)
		taskDefault.Model = strings.TrimSpace(taskDefault.Model)
		if taskDefault.ReasoningEffort != nil {
			effort := strings.TrimSpace(*taskDefault.ReasoningEffort)
			taskDefault.ReasoningEffort = &effort
			if effort == "" {
				taskDefault.ReasoningEffort = nil
			}
		}
		value.TaskDefault = &taskDefault
	}
	if value.ReasoningEffort != nil {
		effort := strings.TrimSpace(*value.ReasoningEffort)
		if effort == "" {
			value.ReasoningEffort = nil
		} else {
			value.ReasoningEffort = &effort
		}
	}
	return value
}
func (t TaskModelSelection) Validate() error {
	for _, value := range []*uint64{t.ContextWindow, t.MaxOutputTokens} {
		if value != nil && (*value == 0 || *value > core.MaxSafeInteger) {
			return errors.New("task token overrides must be positive supported integers")
		}
	}
	if t.ReasoningEffort != nil && len(*t.ReasoningEffort) > 128 {
		return errors.New("task reasoning effort exceeds supported length")
	}
	if !core.Identifier(t.ProfileID) || strings.TrimSpace(t.Model) == "" || len(t.Model) > 256 {
		return errors.New("task default requires a Provider and concrete model")
	}
	return nil
}
func (s AssistantSelection) Validate() error {
	if !core.Identifier(s.ProfileID) || s.ProfileRevision == 0 || s.ProfileRevision > core.MaxSafeInteger || strings.TrimSpace(s.Model) == "" || len(s.Model) > 256 {
		return errors.New("assistant requires a Provider revision and concrete model")
	}
	for _, value := range []*uint64{s.ContextWindow, s.MaxOutputTokens} {
		if value != nil && (*value == 0 || *value > core.MaxSafeInteger) {
			return errors.New("assistant token overrides must be positive supported integers")
		}
	}
	if s.ReasoningEffort != nil && len(*s.ReasoningEffort) > 128 {
		return errors.New("reasoning effort exceeds supported length")
	}
	if s.TaskDefault != nil {
		if err := s.TaskDefault.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (p *ProfileStore) ResolveAssistant(selection AssistantSelection) (core.ResolvedSpec, error) {
	if err := selection.Validate(); err != nil {
		return core.ResolvedSpec{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var profile *core.Profile
	protocol := ""
	for _, value := range p.state.Profiles {
		if value.ID == selection.ProfileID {
			body, _ := json.Marshal(value)
			var copy core.Profile
			if err := core.DecodeJSON(body, &copy); err != nil {
				return core.ResolvedSpec{}, err
			}
			profile = &copy
			break
		}
	}
	if profile == nil {
		return core.ResolvedSpec{}, errors.New("assistant Provider is unavailable")
	}
	if profile.Revision != selection.ProfileRevision {
		return core.ResolvedSpec{}, errors.New("Provider configuration changed; reload before selecting it")
	}
	for _, connection := range p.state.Connections {
		if connection.ProfileID == profile.ID {
			protocol = connection.Protocol
			break
		}
	}
	sameModel := selection.Model == profile.Model
	profile.Model = selection.Model
	if !sameModel {
		var catalog *string
		if profile.ModelSettings != nil {
			catalog = profile.ModelSettings.CatalogProvider
		}
		var inputs map[string][]string
		if profile.ModelSettings != nil {
			inputs = profile.ModelSettings.InputModalities
		}
		profile.ModelSettings = &core.ModelSettings{CatalogProvider: catalog, InputModalities: inputs}
	} else if profile.ModelSettings == nil {
		window, output := profile.Params.Agent.ContextWindow, profile.Params.MaxOutputTokens
		profile.ModelSettings = &core.ModelSettings{ContextWindow: &window, MaxOutputTokens: &output}
	}
	if selection.ContextWindow != nil {
		profile.ModelSettings.ContextWindow = selection.ContextWindow
	}
	if selection.MaxOutputTokens != nil {
		profile.ModelSettings.MaxOutputTokens = selection.MaxOutputTokens
	}
	if selection.ReasoningEffort != nil {
		value := strings.TrimSpace(*selection.ReasoningEffort)
		if value == "" {
			profile.Params.ReasoningEffort = nil
		} else {
			profile.Params.ReasoningEffort = &value
		}
	}
	defaults, err := p.defaultAgentSettings()
	if err != nil {
		return core.ResolvedSpec{}, err
	}
	profile.Params.Agent.Plugins = defaults.Plugins
	profile.Params.Agent.BuiltinTools = defaults.BuiltinTools
	profile.Params.Agent.AllowedTools = nil
	resolved, err := p.applyModelMetadata(*profile)
	if err != nil {
		return core.ResolvedSpec{}, err
	}
	spec, err := resolved.Resolve()
	if err != nil {
		return spec, err
	}
	spec.GatewayProtocol = protocol
	err = spec.Rehash()
	return spec, err
}
func (s *Service) resolveTaskDefault(ctx context.Context, selection TaskModelSelection) (core.ResolvedSpec, error) {
	if err := selection.Validate(); err != nil {
		return core.ResolvedSpec{}, err
	}
	var profile *core.Profile
	for _, value := range s.Profiles.savedSnapshot().Profiles {
		if value.ID == selection.ProfileID {
			copy := value
			profile = &copy
			break
		}
	}
	if profile == nil {
		return core.ResolvedSpec{}, errors.New("Task default Provider is unavailable")
	}
	spec, err := s.Profiles.ResolveAssistant(AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: selection.Model, ContextWindow: selection.ContextWindow, MaxOutputTokens: selection.MaxOutputTokens, ReasoningEffort: selection.ReasoningEffort})
	if err != nil {
		return core.ResolvedSpec{}, err
	}
	if err := s.Profiles.sourceAvailable(ctx, spec.Provider); err != nil {
		return core.ResolvedSpec{}, err
	}
	return spec, nil
}
func (s *Service) configurationView(state assistant.ConfigurationState) (AssistantConfiguration, error) {
	view := AssistantConfiguration{Owner: state.Owner, Generation: state.Generation, Config: state.Config, Revision: state.Revision, Pending: state.Pending}
	if len(state.Intent) > 0 && string(state.Intent) != "null" {
		if err := core.DecodeJSON(state.Intent, &view.Selection); err != nil {
			return view, err
		}
	} else if state.Config != nil {
		view.Selection = AssistantSelection{ProfileID: state.Config.SourceProfileID, ProfileRevision: state.Config.SourceProfileRevision, Model: state.Config.Model}
	} else {
		profiles := s.Profiles.savedSnapshot()
		for _, profile := range profiles.Profiles {
			if profile.ID == profiles.DefaultProviderID {
				view.Selection = AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: profile.Model}
				break
			}
		}
	}
	return view, nil
}
func (s *Service) AssistantConfiguration(ctx context.Context, owner string) (AssistantConfiguration, error) {
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return AssistantConfiguration{}, err
	}
	state, err := c.Configuration(ctx)
	if err != nil {
		return AssistantConfiguration{}, err
	}
	return s.configurationView(state)
}
func (s *Service) SaveAssistantConfiguration(ctx context.Context, owner string, request SaveAssistantConfiguration) (AssistantConfiguration, error) {
	if !core.Identifier(request.RequestID) {
		return AssistantConfiguration{}, errors.New("configuration request identity required")
	}
	request.Selection = normalizedSelection(request.Selection)
	if err := request.Selection.Validate(); err != nil {
		return AssistantConfiguration{}, err
	}
	hash, err := core.HashJSON([]any{request.ExpectedGeneration, request.ExpectedRevision, request.Selection})
	if err != nil {
		return AssistantConfiguration{}, err
	}
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return AssistantConfiguration{}, err
	}
	if receipt, err := c.ConfigurationReceipt(ctx, request.RequestID, hash); err != nil {
		return AssistantConfiguration{}, err
	} else if receipt != nil {
		return s.configurationView(*receipt)
	}
	if request.Selection.TaskDefault != nil {
		if _, err := s.resolveTaskDefault(ctx, *request.Selection.TaskDefault); err != nil {
			return AssistantConfiguration{}, err
		}
	}
	spec, err := s.Profiles.ResolveAssistant(request.Selection)
	if err != nil {
		return AssistantConfiguration{}, err
	}
	intent, err := json.Marshal(request.Selection)
	if err != nil {
		return AssistantConfiguration{}, err
	}
	state, err := c.QueueConfiguration(ctx, assistant.ConfigurationChange{RequestID: request.RequestID, InputHash: hash, ExpectedGeneration: request.ExpectedGeneration, ExpectedRevision: request.ExpectedRevision, Spec: spec, Intent: intent})
	if err != nil {
		return AssistantConfiguration{}, err
	}
	s.Changes.Notify("assistants")
	return s.configurationView(state)
}
func (s *Service) refreshAssistantConfiguration(ctx context.Context, current core.Thread, intent json.RawMessage) (*core.ResolvedSpec, error) {
	selection := AssistantSelection{ProfileID: current.Config.SourceProfileID, ProfileRevision: current.Config.SourceProfileRevision, Model: current.Config.Model}
	if len(intent) > 0 && string(intent) != "null" {
		if err := core.DecodeJSON(intent, &selection); err != nil {
			return nil, err
		}
	}
	// A changed saved Provider revision affects future explicit selections only.
	// Public metadata may refresh concrete defaults for the same immutable intent.
	// This boundary only checks immutable selection identity. Building the UI's
	// entire model directory here delays queued input and result processing.
	sameRevision := false
	s.Profiles.mu.Lock()
	for _, profile := range s.Profiles.state.Profiles {
		if profile.ID == selection.ProfileID && profile.Revision == selection.ProfileRevision {
			sameRevision = true
			break
		}
	}
	s.Profiles.mu.Unlock()
	if !sameRevision {
		if err := s.Profiles.sourceAvailable(ctx, current.Config.Provider); err != nil {
			return nil, err
		}
		// Keep the original model selection while adopting product tool defaults
		// only at a fresh turn boundary, including older Provider revisions.
		settings, err := s.Profiles.defaultAgentSettings()
		if err != nil {
			return nil, err
		}
		next := current.Config
		next.Params.Agent.BuiltinTools = settings.BuiltinTools
		next.Params.Agent.AllowedTools = nil
		next.Params.Agent.Plugins = settings.Plugins
		next.Params.Agent.RequestTimeoutMS = settings.RequestTimeoutMS
		for name, configured := range next.Params.Agent.Plugins {
			configured.Source = nil
			next.Params.Agent.Plugins[name] = configured
		}
		if err := next.Rehash(); err != nil {
			return nil, err
		}
		return &next, nil
	}
	next, err := s.Profiles.ResolveAssistant(selection)
	if err != nil {
		return nil, err
	}
	return &next, nil
}

// CreateAssistant persists initial selection and rules before publishing identity.
// Retrying creation never re-resolves defaults or rewrites an existing assistant.
type CreateAssistant struct {
	RequestID string              `json:"request_id" required:"true"`
	Name      string              `json:"name,omitempty"`
	Selection *AssistantSelection `json:"selection,omitempty"`
	Text      string              `json:"text,omitempty"`
	Locale    string              `json:"locale,omitempty"`
}

func (s *Service) CreateAssistant(ctx context.Context, request CreateAssistant) (assistant.Definition, error) {
	return s.createAssistant(ctx, request, false)
}

// BootstrapAssistant creates Pot only when this Space has a Provider and no
// assistants. The registry transaction arbitrates simultaneous windows/retries.
func (s *Service) BootstrapAssistant(ctx context.Context, locale string) (map[string]any, error) {
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return nil, err
	}
	if len(definitions) > 0 {
		return map[string]any{"assistant": definitions[0]}, nil
	}
	history, err := s.Registry.HasHistory(ctx)
	if err != nil {
		return nil, err
	}
	if history || s.Profiles.savedSnapshot().DefaultProviderID == "" {
		return map[string]any{"assistant": nil, "initialized": history}, nil
	}
	definition, err := s.createAssistant(ctx, CreateAssistant{RequestID: storage.ID(), Locale: locale}, true)
	return map[string]any{"assistant": definition}, err
}

func (s *Service) createAssistant(ctx context.Context, request CreateAssistant, onlyFirst bool) (assistant.Definition, error) {
	if request.Locale != "" && request.Locale != "en" && request.Locale != "zh-CN" {
		return assistant.Definition{}, errors.New("unsupported assistant locale")
	}
	welcome := webassets.Translate(request.Locale, "assistant.welcome")
	var intent json.RawMessage
	var err error
	if request.Selection != nil {
		normalized := normalizedSelection(*request.Selection)
		request.Selection = &normalized
		if err = request.Selection.Validate(); err != nil {
			return assistant.Definition{}, err
		}
		intent, err = json.Marshal(request.Selection)
		if err != nil {
			return assistant.Definition{}, err
		}
	}
	binding, err := core.HashJSON(request.Selection)
	if err != nil {
		return assistant.Definition{}, err
	}
	var definition assistant.Definition
	err = s.Machines.OwnerAction(func() error {
		var err error
		definition, err = s.Registry.CreateWithIntent(ctx, request.RequestID, request.Name, binding, request.Text, intent, welcome, onlyFirst, func() (*core.ResolvedSpec, error) {
			if request.Selection != nil {
				if request.Selection.TaskDefault != nil {
					if _, err := s.resolveTaskDefault(ctx, *request.Selection.TaskDefault); err != nil {
						return nil, err
					}
				}
				spec, err := s.Profiles.ResolveAssistant(*request.Selection)
				return &spec, err
			}
			if s.Profiles.savedSnapshot().DefaultProviderID == "" {
				return nil, nil
			}
			spec, err := s.Profiles.Resolve("")
			return &spec, err
		})
		return err
	})
	if err != nil {
		return definition, err
	}
	if _, err = s.Conversation(ctx, definition.ID); err != nil {
		return definition, err
	}
	s.Changes.Notify("assistants")
	return definition, nil
}
