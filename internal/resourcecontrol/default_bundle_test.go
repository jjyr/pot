package resourcecontrol

import (
	"context"
	"testing"
)

func TestChangedCatalogDefaultKeepsExistingSandboxPinAfterRestart(t *testing.T) {
	ctx := context.Background()
	options, closeStore := fixture(t, nil)
	defer closeStore()
	m, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	original, err := m.Request(ctx, "existingSandbox", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(original.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	options.Catalog.DefaultLinuxBundle = "other"
	m, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	for _, scope := range [][2]string{{"", ""}, {"owner", "turn"}} {
		got, err := m.requestFor(ctx, "existingSandbox", "", scope[0], scope[1])
		if err != nil || got.BundleID != original.BundleID {
			t.Fatalf("existing pin changed: %+v %v", got, err)
		}
	}
	if _, err := m.Request(ctx, "existingSandbox", "other"); err == nil {
		t.Fatal("explicit replacement changed an existing pin")
	}
	fresh, err := m.Request(ctx, "newSandbox", "")
	if err != nil || fresh.BundleID != "other" {
		t.Fatalf("new Sandbox did not use new default: %+v %v", fresh, err)
	}
}
