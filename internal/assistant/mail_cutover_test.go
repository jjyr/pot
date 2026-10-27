package assistant

import (
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestCutoverMailBatchRetirementAndOrdering(t *testing.T) {
	for _, state := range []string{"queued", "processing", "interrupted", "waiting"} {
		for _, retired := range []bool{false, true} {
			name := state + "/active"
			if retired {
				name = state + "/retired"
			}
			t.Run(name, func(t *testing.T) {
				m, _ := mailbox(t, "local")
				old := protocol.Envelope{Kind: "message", ID: "old", From: address("remote", "sender"), To: address("local", "owner"), ConversationID: "26c8d41a-3a1c-41a3-912e-7b12316fe901", Text: "old history"}
				deliver(t, m, old)
				if _, err := m.db.Exec("UPDATE agent_messages SET state=? WHERE id=?", state, old.ID); err != nil {
					t.Fatal(err)
				}
				if retired {
					if _, err := m.db.Exec("INSERT INTO retired_agent_link_work VALUES('message',?)", old.ID); err != nil {
						t.Fatal(err)
					}
				}
				fresh := old
				fresh.ID = "new"
				fresh.ConversationID = "26c8d41a-3a1c-41a3-912e-7b12316fe902"
				fresh.Text = "fresh work"
				deliver(t, m, fresh)
				seed, err := m.Next(t.Context(), "owner")
				if err != nil {
					t.Fatal(err)
				}
				if !retired && state != "queued" {
					if seed != nil {
						t.Fatal("active uncertain group lost its fence", seed)
					}
					return
				}
				if seed == nil {
					t.Fatal("fresh work blocked")
				}
				batch, err := m.ClaimMailBatch(t.Context(), *seed)
				if err != nil {
					t.Fatal(err)
				}
				if retired {
					if seed.ID != fresh.ID || len(batch.Messages) != 1 {
						t.Fatal(batch)
					}
				} else if seed.ID != old.ID || len(batch.Messages) != 2 || batch.Messages[1].ID != fresh.ID {
					t.Fatal("normal ordering changed", batch)
				}
				if retired {
					var savedState, body string
					if err = m.db.QueryRow("SELECT state,envelope FROM agent_messages WHERE id=?", old.ID).Scan(&savedState, &body); err != nil {
						t.Fatal(err)
					}
					expected, _ := json.Marshal(old)
					if savedState != state || body != string(expected) {
						t.Fatal("retired history changed", savedState, body)
					}
				}
			})
		}
	}
	// The cutover marker also records local history, which remains locally usable.
	t.Run("local-history", func(t *testing.T) {
		m, _ := mailbox(t, "local")
		msg := sendMail(t, m, "local-mail", address("local", "alice"), address("local", "bob"), nil)
		if _, err := m.db.Exec("INSERT INTO retired_agent_link_work VALUES('message',?)", msg.ID); err != nil {
			t.Fatal(err)
		}
		next, err := m.Next(t.Context(), "bob")
		if err != nil || next == nil {
			t.Fatal(next, err)
		}
		batch, err := m.ClaimMailBatch(t.Context(), *next)
		if err != nil || len(batch.Messages) != 1 {
			t.Fatal(batch, err)
		}
	})
}
