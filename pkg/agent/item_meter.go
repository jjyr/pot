package agent

import (
	"encoding/json"

	m "github.com/jjyr/pot/pkg/modelprotocol"
)

// ContentEstimateMethod identifies the estimation strategy used for single-record
// content sizing. These estimates describe the per-record content footprint only;
// they are not billing figures and do not account for repeated context cost.
const ContentEstimateMethod = "codex-content-v1"

// EstimateTextTokens estimates tokens for plain text using the package's
// byte-based token heuristic. This is a single content estimate, not a billing
// value and not a repeated-context cost.
func EstimateTextTokens(text string) uint64 {
	return tokens(len(text))
}

// EstimateToolCallTokens estimates tokens for a tool call, counting the name,
// function metadata, and arguments as originally rendered. Arguments are not
// compacted because context accounting reflects the original JSON text size.
func EstimateToolCallTokens(call m.ToolCall) uint64 {
	return callTokens(call.Name, string(call.Arguments))
}

// EstimateToolResultTokens estimates tokens for a single tool result record.
// It splits any image content out of the raw tool JSON via toolResultMessage and
// delegates to estimateLogicalMessage. Errors are returned so callers can mark
// the estimate as unknown; they must never abort actual tool execution because
// of an estimation failure.
func EstimateToolResultTokens(callID string, raw json.RawMessage) (uint64, error) {
	msg, err := toolResultMessage(callID, raw, true)
	if err != nil {
		return 0, err
	}
	return estimateLogicalMessage(msg)
}

// estimateLogicalMessage computes a single-record size estimate for a message
// using the package's content-based fallback strategy. It does not represent
// billing costs or repeated context cost.
func estimateLogicalMessage(message m.Message) (uint64, error) {
	text, err := message.TextWithReferences()
	if err != nil {
		return 0, err
	}
	size := len(text) + len(message.Images)*7373
	for _, doc := range message.Documents {
		size += max(7373, len(doc.Data)*3/4)
	}
	if message.Role == "tool" {
		size += len(message.CallID)
	}
	total := tokens(size)
	for _, call := range message.Calls {
		total += callTokens(call.Name, string(call.Arguments))
	}
	return total, nil
}
