package resourcecontrol

import (
	"context"
	"runtime"
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
	options.Catalog.Defaults["fixture"][runtime.GOARCH] = "other"
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

func TestDifferentProviderCannotUseAnotherProvidersBundle(t *testing.T) {
	ctx := context.Background()
	options, closeStore := fixture(t, nil)
	defer closeStore()
	b := options.Catalog.Bundles["bundle"]
	b.Provider = "alternate"
	options.Catalog.Bundles["alternate-bundle"] = b
	options.Catalog.Defaults["alternate"] = map[string]string{runtime.GOARCH: "alternate-bundle"}
	options.SandboxProvider = func(_ context.Context, id string) (string, error) {
		if id == "alternate-sandbox" {
			return "alternate", nil
		}
		return "fixture", nil
	}
	manager, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(ctx)
	if _, err = manager.Request(ctx, "alternate-sandbox", "bundle"); err == nil {
		t.Fatal("cross-provider resource pin accepted")
	}
	request, err := manager.Request(ctx, "alternate-sandbox", "")
	if err != nil || request.BundleID != "alternate-bundle" {
		t.Fatal(request, err)
	}
	status, err := manager.Snapshot(ctx)
	if err != nil || status.DefaultBundles["alternate"] != "alternate-bundle" || status.DefaultBundles["fixture"] != "bundle" {
		t.Fatal(status, err)
	}
}
