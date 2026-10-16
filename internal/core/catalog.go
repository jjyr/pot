package core

import (
	"encoding/json"
	"fmt"
	"slices"
)

type ToolOverride struct {
	Description *string `json:"description"`
}
type ModelMessages struct {
	InstructionsTemplate *string                 `json:"instructions_template"`
	Tools                map[string]ToolOverride `json:"tools"`
}
type ModelDescriptor struct {
	InputModalities             []string      `json:"input_modalities"`
	SupportsImageDetailOriginal bool          `json:"supports_image_detail_original"`
	SupportedInAPI              bool          `json:"supported_in_api"`
	Visibility                  string        `json:"visibility"`
	Priority                    uint32        `json:"priority"`
	ModelMessages               ModelMessages `json:"model_messages"`
}

func DefaultModelDescriptor() ModelDescriptor {
	return ModelDescriptor{InputModalities: []string{"text"}, SupportedInAPI: true, Visibility: "list", Priority: 100, ModelMessages: ModelMessages{Tools: map[string]ToolOverride{}}}
}
func (d ModelDescriptor) Vision() bool { return slices.Contains(d.InputModalities, "image") }
func (d ModelDescriptor) Validate() error {
	if !slices.Contains(d.InputModalities, "text") || d.SupportsImageDetailOriginal && !d.Vision() || !slices.Contains([]string{"list", "hide", "none"}, d.Visibility) {
		return fmt.Errorf("invalid model descriptor")
	}
	seen := map[string]bool{}
	for _, m := range d.InputModalities {
		if !slices.Contains([]string{"text", "image", "audio"}, m) || seen[m] {
			return fmt.Errorf("invalid/duplicate modality")
		}
		seen[m] = true
	}
	return nil
}

type ModelCatalog struct {
	SchemaVersion uint32                                `json:"schema_version" required:"true"`
	Default       json.RawMessage                       `json:"default" required:"true"`
	Models        map[string]map[string]json.RawMessage `json:"models"`
}

func (c ModelCatalog) Resolve(provider, model string) (ModelDescriptor, error) {
	if c.SchemaVersion != 1 {
		return ModelDescriptor{}, fmt.Errorf("unsupported catalog schema")
	}
	defaults, err := parseJSON(c.Default)
	if err != nil {
		return ModelDescriptor{}, err
	}
	if _, ok := defaults.(map[string]any); !ok {
		return ModelDescriptor{}, fmt.Errorf("catalog default must be an object")
	}
	data, _ := json.Marshal(DefaultModelDescriptor())
	effective, _ := parseJSON(data)
	effective = MergeJSON(effective, defaults)
	if override, found := c.Models[provider][model]; found {
		value, err := parseJSON(override)
		if err != nil {
			return ModelDescriptor{}, err
		}
		if _, ok := value.(map[string]any); !ok {
			return ModelDescriptor{}, fmt.Errorf("model override must be an object")
		}
		effective = MergeJSON(effective, value)
	}
	data, err = json.Marshal(effective)
	if err != nil {
		return ModelDescriptor{}, err
	}
	var d ModelDescriptor
	if err = DecodeJSON(data, &d); err != nil {
		return d, err
	}
	return d, d.Validate()
}
func (c ModelCatalog) Validate() error {
	if _, err := c.Resolve("", ""); err != nil {
		return err
	}
	for provider, models := range c.Models {
		for model := range models {
			if _, err := c.Resolve(provider, model); err != nil {
				return err
			}
		}
	}
	return nil
}

// MergeJSON recursively applies non-null overrides. Empty arrays, false, zero
// and empty strings are explicit values; empty objects retain inherited keys.
func MergeJSON(base, override any) any {
	if override == nil {
		return base
	}
	a, aok := base.(map[string]any)
	b, bok := override.(map[string]any)
	if aok && bok {
		for key, v := range b {
			if v != nil {
				a[key] = MergeJSON(a[key], v)
			}
		}
		return a
	}
	return override
}
