package resourcecontrol

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/resource"
	"net/http"
	"testing"
)

func TestCachePinsPruneConfirmedDeletionAndRestart(t *testing.T) {
	ctx := context.Background()
	options, closeStore := fixture(t, nil)
	defer closeStore()
	m, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	r := options.Catalog.Resources["asset"]
	if _, err = options.Store.Prepare(ctx, r, "https://example.test", func(resource.Progress) {}); err != nil {
		t.Fatal(err)
	}
	status, err := m.Snapshot(ctx)
	if err != nil || !status.Cache.Items[0].Prunable || status.Cache.Items[0].DiskBytes < r.SizeBytes {
		t.Fatal(status, err)
	}
	if _, err = m.Request(ctx, "saved", ""); err != nil {
		t.Fatal(err)
	}
	// An alternate catalog ID with the same digest cannot bypass a pin.
	options.Catalog.Resources["alias"] = r
	if err = m.Prune(ctx, "alias"); err == nil {
		t.Fatal("digest alias bypassed Sandbox reference")
	}
	if err = m.ForgetAfter(ctx, "saved", func() error { return errors.New("delete failed") }); err == nil {
		t.Fatal("failed deletion accepted")
	}
	if err = m.Prune(ctx, "asset"); err == nil {
		t.Fatal("failed deletion released reference")
	}
	if err = m.ForgetAfter(ctx, "saved", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	m, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	status, err = m.Snapshot(ctx)
	if err != nil || len(status.Requests) != 0 || !status.Cache.Items[0].Prunable {
		t.Fatal(status, err)
	}
	if err = m.Prune(ctx, "asset"); err != nil {
		t.Fatal(err)
	}
	status, err = m.Snapshot(ctx)
	if err != nil || status.Cache.Items[0].Present || status.Bundles[0].Ready {
		t.Fatal(status, err)
	}
}
func TestActivePreparationAndUnknownDefinitionCannotReleaseResources(t *testing.T) {
	ctx := context.Background()
	options, closeStore := fixture(t, roundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }))
	defer closeStore()
	options.ValidateSandbox = func(_ context.Context, id string) error {
		if id != "saved" {
			return errors.New("missing definition")
		}
		return nil
	}
	m, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(ctx)
	if _, err = m.Request(ctx, "unknown", ""); err == nil {
		t.Fatal("unknown Sandbox pinned resources")
	}
	r, err := m.Request(ctx, "saved", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(r.ID); err != nil {
		t.Fatal(err)
	}
	removed := false
	if err = m.ForgetAfter(ctx, "saved", func() error { removed = true; return nil }); err == nil || removed {
		t.Fatal("active preparation deleted definition")
	}
	if err = m.Prune(ctx, "asset"); err == nil {
		t.Fatal("active preparation pruned cache")
	}
	if _, err = m.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
}
