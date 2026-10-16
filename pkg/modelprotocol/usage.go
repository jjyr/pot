package modelprotocol

import (
	"encoding/json"
	"fmt"
)

type UsageObserver struct {
	anthropicInput *uint64
	anthropicCache uint64
}

func (o *UsageObserver) Observe(protocol Protocol, raw json.RawMessage) (*UsageReport, error) {
	event, err := object(raw)
	if err != nil {
		return nil, err
	}
	return o.observe(protocol, event)
}
func (o *UsageObserver) observe(protocol Protocol, event map[string]any) (*UsageReport, error) {
	report := &UsageReport{}
	switch protocol {
	case OpenAIChat:
		usage := obj(event["usage"])
		if usage == nil {
			return nil, nil
		}
		report.InputTokens = uintValue(usage["prompt_tokens"])
		report.OutputTokens = uintValue(usage["completion_tokens"])
		report.CacheReadTokens = uintValue(usage["prompt_cache_hit_tokens"])
		if report.CacheReadTokens == nil {
			report.CacheReadTokens = uintValue(get(usage, "prompt_tokens_details", "cached_tokens"))
		}
	case OpenAIResponses:
		switch str(event["type"]) {
		case "response.completed", "response.incomplete", "response.failed":
		default:
			return nil, nil
		}
		usage := obj(get(event, "response", "usage"))
		if usage == nil {
			return nil, nil
		}
		report.InputTokens = uintValue(usage["input_tokens"])
		report.OutputTokens = uintValue(usage["output_tokens"])
		report.CacheReadTokens = uintValue(get(usage, "input_tokens_details", "cached_tokens"))
	case AnthropicMessages:
		if event["type"] == "message_start" {
			usage := obj(get(event, "message", "usage"))
			if n := uintValue(usage["input_tokens"]); n != nil {
				cache := uintValue(usage["cache_read_input_tokens"])
				write := uintValue(usage["cache_creation_input_tokens"])
				o.anthropicCache = 0
				if cache != nil {
					o.anthropicCache = *cache
				}
				total, err := sum(*n, o.anthropicCache)
				if err != nil {
					return nil, err
				}
				if write != nil {
					total, err = sum(total, *write)
					if err != nil {
						return nil, err
					}
				}
				o.anthropicInput = integer(total)
			}
			report.InputTokens = o.anthropicInput
			report.CacheReadTokens = integer(o.anthropicCache)
		} else if event["type"] == "message_delta" && obj(event["usage"]) != nil {
			report.InputTokens = o.anthropicInput
			report.OutputTokens = uintValue(get(event, "usage", "output_tokens"))
			report.CacheReadTokens = integer(o.anthropicCache)
		} else {
			return nil, nil
		}
	case Gemini:
		usage := obj(event["usageMetadata"])
		if usage == nil {
			return nil, nil
		}
		report.InputTokens = uintValue(usage["promptTokenCount"])
		report.CacheReadTokens = uintValue(usage["cachedContentTokenCount"])
		report.OutputTokens = uintValue(usage["candidatesTokenCount"])
		if report.OutputTokens != nil {
			if thoughts := uintValue(usage["thoughtsTokenCount"]); thoughts != nil {
				total, err := sum(*report.OutputTokens, *thoughts)
				if err != nil {
					return nil, err
				}
				report.OutputTokens = integer(total)
			}
		}
	case Unknown:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported usage protocol")
	}
	return report, nil
}

// Clamp preserves native fields and only bounds output, requesting usage where
// supported. Missing/null caps inherit the policy cap; malformed caps fail closed.
func (p Protocol) Clamp(raw json.RawMessage, cap uint64) (json.RawMessage, uint64, error) {
	if err := p.Validate(); err != nil {
		return nil, 0, err
	}
	if cap == 0 {
		return nil, 0, fmt.Errorf("invalid output cap")
	}
	body, err := object(raw)
	if err != nil {
		return nil, 0, err
	}
	if p == Unknown {
		return raw, cap, nil
	}
	target := body
	field := "max_tokens"
	switch p {
	case OpenAIChat:
		options, exists := body["stream_options"]
		if !exists {
			options = map[string]any{}
			body["stream_options"] = options
		}
		if obj(options) == nil {
			return nil, 0, fmt.Errorf("invalid stream options")
		}
		obj(options)["include_usage"] = true
		if _, exists := body["max_completion_tokens"]; exists {
			field = "max_completion_tokens"
			if old := uintValue(body["max_tokens"]); old != nil {
				current := uintValue(body[field])
				if current == nil || *old < *current {
					body[field] = json.Number(fmt.Sprint(*old))
				}
			}
			delete(body, "max_tokens")
		}
	case OpenAIResponses:
		field = "max_output_tokens"
	case Gemini:
		generation, exists := body["generationConfig"]
		if !exists {
			generation = map[string]any{}
			body["generationConfig"] = generation
		}
		if obj(generation) == nil {
			return nil, 0, fmt.Errorf("invalid generation config")
		}
		target = obj(generation)
		field = "maxOutputTokens"
	}
	output := cap
	if target[field] != nil {
		n := uintValue(target[field])
		if n == nil {
			return nil, 0, fmt.Errorf("invalid output cap")
		}
		output = *n
		if output < 1 {
			output = 1
		}
		if output > cap {
			output = cap
		}
	}
	target[field] = output
	data, err := json.Marshal(body)
	return data, output, err
}
