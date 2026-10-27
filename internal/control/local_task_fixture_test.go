package control

import (
	"context"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/execution"
	"testing"
)

func taskControl(t *testing.T, id, url string) *Service {
	t.Helper()
	manager, _ := execution.New(id, "boot-"+id, execution.Limits{MaxActive: 1, MaxQueued: 8})
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Root: t.TempDir(), RuntimeID: id, BootID: "boot-" + id, Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if url != "" {
		if err = s.Permissions.Configure(id, MachinePolicy{ExecutionAccess: "allow"}); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		legacyMainAssistant(ctx, s, "create", "Pot")
		profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "test", Name: "Test", Model: "model", Endpoint: url, Protocol: "openai_chat", APIKey: "owner-key", MaxOutputTokens: 100})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := s.Profiles.Resolve(profile.ID)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := s.Conversation(ctx, "main")
		if err = c.Configure(ctx, spec, 0); err != nil {
			t.Fatal(err)
		}
	}
	return s
}
