package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"testing"
)

func TestKnownRejectedModelClearsFenceWhileUnknownTransportPreservesIt(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		cleared bool
	}{{"rejected", &core.ModelRequestError{Cause: errors.New("provider rejected"), Disposition: "rejected"}, true}, {"unsent", &core.ModelRequestError{Cause: errors.New("configuration invalid"), Disposition: "unsent"}, true}, {"network lost", errors.New("transport result unknown"), false}} {
		t.Run(test.name, func(t *testing.T) {
			a, f, s, thread, execution := setup(t)
			s.model = func(core.ModelRequest, int) (string, error) { return "", test.err }
			if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); !errors.Is(err, test.err) {
				t.Fatal(err)
			}
			var checkpoint Checkpoint
			if err := json.Unmarshal(f.body, &checkpoint); err != nil {
				t.Fatal(err)
			}
			if (checkpoint.ModelRequestID == nil) != test.cleared {
				t.Fatal("incorrect model request uncertainty", checkpoint.ModelRequestID)
			}
		})
	}
}
