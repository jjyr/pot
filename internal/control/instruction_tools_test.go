package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"testing"
	"time"
)

func TestInstructionToolsAlwaysRequireExactOwnerReview(t *testing.T) {
	for _, policy := range []string{"allow", "deny"} {
		t.Run(policy, func(t *testing.T) {
			s, _ := configurationService(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "create", Name: "Rules"})
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.Conversation(ctx, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			thread, _ := c.Thread(ctx)
			if err = s.Permissions.Configure("runtime", MachinePolicy{"sandbox", policy}); err != nil {
				t.Fatal(err)
			}
			if len(s.productTools(true)) != 0 {
				t.Fatal("Worker inherited owner instruction tools")
			}
			sub := s.Permissions.Changes.Subscribe()
			defer sub.Close()
			done := make(chan error, 1)
			go func() {
				_, e := s.instructionCall(ctx, a.ID, "turn", thread.Generation, modelprotocol.ToolCall{ID: "change", Name: "update_agent_instructions", Arguments: json.RawMessage(`{"text":"owner reviewed"}`)})
				done <- e
			}()
			var approval Approval
			for {
				pending := s.Permissions.Pending(a.ID)
				if len(pending) > 0 {
					approval = pending[0]
					break
				}
				if _, err = sub.Next(ctx); err != nil {
					t.Fatal("mandatory review missing", err)
				}
			}
			before, _ := c.Instructions(ctx)
			if before.Text == "owner reviewed" {
				t.Fatal("rules saved before review")
			}
			if err = s.Permissions.Decide("different-owner", approval.ID, "allow_once"); err == nil {
				t.Fatal("cross-owner approval")
			}
			if err = s.Permissions.Decide(a.ID, approval.ID, "allow_once"); err != nil {
				t.Fatal(err)
			}
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			saved, _ := c.Instructions(ctx)
			if saved.Text != "owner reviewed" {
				t.Fatal(saved)
			}
		})
	}
}
