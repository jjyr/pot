package systemnotifications

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAutomaticAuthorizationEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, status, surface string
		enabled               bool
		want                  int32
	}{
		{"first main", "default", "main", true, 1},
		{"tray initialization", "default", "tray", true, 0},
		{"granted", "granted", "main", true, 0},
		{"denied", "denied", "main", true, 0},
		{"off", "default", "main", false, 0},
		{"unknown", "unavailable", "main", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "notifications.json")
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.SetEnabled(tc.enabled); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			a := &Authorization{Store: store, Status: func() string { return tc.status }, Request: func() error { requests.Add(1); return nil }}
			var workers sync.WaitGroup
			for range 20 {
				workers.Go(func() {
					if err := a.MainReady(tc.surface); err != nil {
						t.Error(err)
					}
				})
			}
			workers.Wait()
			if requests.Load() != tc.want {
				t.Fatalf("requests %d, want %d", requests.Load(), tc.want)
			}
			if tc.surface == "tray" {
				if err = a.MainReady("main"); err != nil {
					t.Fatal(err)
				}
				if requests.Load() != 1 {
					t.Fatal("tray consumed main readiness")
				}
				return
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			next := &Authorization{Store: reopened, Status: func() string { return "default" }, Request: func() error { requests.Add(1); return nil }}
			if err = next.MainReady("main"); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != tc.want {
				t.Fatal("restart repeated authorization or ignored off preference")
			}
		})
	}
}

func TestAuthorizationFailureAndManualRetry(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("native failure")

	var requests atomic.Int32
	a := &Authorization{Store: store, Status: func() string { return "default" }, Request: func() error {
		if requests.Add(1) == 1 {
			return failure
		}
		return nil
	}}
	if err = a.MainReady("main"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	before := requests.Load()
	if err = a.MainReady("main"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != before {
		t.Fatal("automatic retry after failure")
	}
	if err = a.Authorize(); err != nil {
		t.Fatal("manual retry blocked", err)
	}
	if requests.Load() != before+1 {
		t.Fatal("manual retry did not run")
	}
}

func TestAuthorizationCannotRequestAfterPersistenceFailure(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "missing", "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &Authorization{Store: store, Status: func() string { return "default" }, Request: func() error { t.Fatal("requested without durable attempt"); return nil }}
	if err = a.MainReady("main"); err == nil {
		t.Fatal("missing persistence error")
	}
	if err = a.MainReady("main"); err != nil {
		t.Fatal("automatic failure repeated", err)
	}
}

func TestMainAndTraySettingsShareAuthorization(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var granted atomic.Bool
	var requests atomic.Int32
	a := &Authorization{Store: store, Status: func() string {
		if granted.Load() {
			return "granted"
		}
		return "default"
	}, Request: func() error {
		requests.Add(1)
		close(entered)
		<-release
		granted.Store(true)
		return nil
	}}
	var workers sync.WaitGroup
	workers.Go(func() {
		if err := a.MainReady("main"); err != nil {
			t.Error(err)
		}
	})
	<-entered
	for range 20 {
		workers.Go(func() {
			if err := a.Authorize(); err != nil {
				t.Error(err)
			}
		})
	}
	close(release)
	workers.Wait()
	if requests.Load() != 1 {
		t.Fatalf("concurrent main/tray requested %d times", requests.Load())
	}
}

func TestManualAuthorizationWithoutPersistence(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "missing", "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	a := &Authorization{Store: store, Status: func() string { return "default" }, Request: func() error { requests++; return nil }}
	if err = a.Authorize(); err != nil {
		t.Fatal(err)
	}
	if err = a.MainReady("main"); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("manual request or automatic suppression failed: %d", requests)
	}
}
