package agenthost

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
)

// Codex 0.163 TurnError carries free-form message/additionalDetails alongside
// codexErrorInfo. Only fixed protocol categories and valid HTTP status numbers
// may enter the existing persisted diagnostic. Unknown shapes remain generic.
func codexTurnFailure(raw json.RawMessage) string {
	const generic = "codex_turn_failed"
	var failure struct {
		Message           string          `json:"message" required:"true"`
		Info              json.RawMessage `json:"codexErrorInfo"`
		AdditionalDetails *string         `json:"additionalDetails"`
		Misalignment      json.RawMessage `json:"misalignment"`
	}
	if core.DecodeJSON(raw, &failure) != nil {
		return generic
	}
	var category string
	if json.Unmarshal(failure.Info, &category) == nil {
		switch category {
		case "contextWindowExceeded", "sessionBudgetExceeded", "usageLimitExceeded", "rateLimitExceeded", "flexUnavailable", "serverOverloaded", "cyberPolicy", "misalignmentPolicyViolation", "tooManyDenials", "internalServerError", "unauthorized", "badRequest", "threadRollbackFailed", "sandboxError", "other":
			return generic + ": " + category
		}
		return generic
	}
	var variants map[string]json.RawMessage
	if json.Unmarshal(failure.Info, &variants) != nil || len(variants) != 1 {
		return generic
	}
	for name, payload := range variants {
		switch name {
		case "httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts":
		default:
			return generic
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(payload, &fields) != nil || fields == nil || len(fields) > 1 {
			return generic
		}
		for key := range fields {
			if key != "httpStatusCode" {
				return generic
			}
		}
		status, present := fields["httpStatusCode"]
		if !present || string(status) == "null" {
			return generic + ": " + name
		}
		var code int
		if json.Unmarshal(status, &code) != nil || code < 100 || code > 599 {
			return generic
		}
		return fmt.Sprintf("%s: %s (http_status=%d)", generic, name, code)
	}
	return generic
}
