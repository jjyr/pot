package agentlink

import "github.com/jjyr/pot/pkg/protocol"

// MCPNativeUserRequest is addressed by the trusted human UI. It is never a model
// tool or ordinary OAuth caller operation. A node receiver executes only its own
// assistant profile/delete backend under the existing revision/cleanup rules.
type MCPNativeUserRequest struct {
	Version    int                         `json:"version"`
	ID         string                      `json:"id"`
	Connection string                      `json:"connection"`
	Epoch      string                      `json:"epoch"`
	Target     protocol.AgentRef           `json:"target"`
	Management *AssistantManagementRequest `json:"management,omitempty"`
	Profile    *ProfileRequest             `json:"profile,omitempty"`
	Delete     *DeleteAssistantRequest     `json:"delete,omitempty"`
}
type MCPNativeUserResult struct {
	ID         string                     `json:"id"`
	Epoch      string                     `json:"epoch"`
	Management *AssistantManagementResult `json:"management,omitempty"`
	Profile    *Profile                   `json:"profile,omitempty"`
	Deleted    *AssistantDeletion         `json:"deleted,omitempty"`
	Error      string                     `json:"error,omitempty"`
}
