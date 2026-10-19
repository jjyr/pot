package agent

import (
	"context"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

// Resolve host-owned references before measuring or sending model input. The
// checkpoint retains internal associations, never an expanded source-text copy.
func (a *Agent) resolveMessages(ctx context.Context, spec core.ResolvedSpec, messages []modelprotocol.Message) ([]modelprotocol.Message, error) {
	var err error
	if a.ResolveReferences != nil {
		messages, err = a.ResolveReferences(ctx, messages)
		if err != nil {
			return nil, err
		}
	}
	if a.ResolveAttachments != nil {
		return a.ResolveAttachments(ctx, spec, messages)
	}
	return messages, nil
}
