package protocol

// AgentExecutionStatus observes an accepted request and its associated task
// tree. Terminal describes execution, never the lifetime of a subscription.
// Reply bodies remain on the conversation stream. Consumers use the boot,
// conversation and cursor together to fence completion against delivered replies.
type AgentExecutionStatus struct {
	BootID             string                   `json:"boot_id"`
	ConversationID     string                   `json:"conversation_id"`
	ConversationCursor int64                    `json:"conversation_cursor"`
	AssistantID        string                   `json:"assistant_id"`
	RequestID          string                   `json:"request_id"`
	MessageID          string                   `json:"message_id"`
	State              string                   `json:"state"`
	Terminal           bool                     `json:"terminal"`
	Error              string                   `json:"error,omitempty"`
	ReplyIDs           []string                 `json:"reply_ids"`
	Turns              []string                 `json:"turns"`
	Assistant          AgentExecutionProgress   `json:"assistant"`
	Tasks              []AgentExecutionProgress `json:"tasks"`
}

type AgentExecutionProgress struct {
	ID          string   `json:"id"`
	RunID       string   `json:"run_id,omitempty"`
	State       string   `json:"state"`
	ActiveTools []string `json:"active_tools"`
}
