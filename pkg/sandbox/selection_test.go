package sandbox

import (
	"context"
	"testing"
)

func TestSelectedSandboxIsPersistentAndNeverReplacedAfterRetirement(t *testing.T) {
	ctx := context.Background()
	manager, provider, _ := setupManager(t, true)
	definition, err := manager.Create(ctx, "second", "Second", platformID(), Resources{})
	if err != nil {
		t.Fatal(err)
	}
	if provider.starts != 0 {
		t.Fatal("creating definition started a guest")
	}
	if err = manager.Select(ctx, definition.ID); err != nil {
		t.Fatal(err)
	}
	selected, err := manager.ResolveSelected(ctx)
	if err != nil || selected.ID != definition.ID {
		t.Fatal(selected, err)
	}
	if _, err = manager.Create(ctx, "second", "Second", platformID(), Resources{}); err != nil {
		t.Fatal("exact definition retry failed", err)
	}
	if _, err = manager.Create(ctx, "second", "Changed", platformID(), Resources{}); err == nil {
		t.Fatal("definition identity overwritten")
	}
	if _, err = manager.Lifecycle(ctx, "second", "delete"); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ResolveSelected(ctx); err == nil {
		t.Fatal("retired selected Sandbox silently replaced")
	}
}
