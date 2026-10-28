package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/pkg/instance"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}
}

func TestDiscoverySeparatesVerifiedReleaseAndInstallationEligibility(t *testing.T) {
	for _, reason := range []string{"eligible", "format", "migration_path", "protocol", "artifact", "no_host", "signature", "same", "older", "network"} {
		t.Run(reason, func(t *testing.T) {
			r := eligibleRelease(t, "0.2.0")
			r.Artifacts = []Artifact{{Kind: "cli", Platform: "linux-amd64", URL: releaseOrigin + "v0.2.0/pot.tar.gz", Size: 1, SHA256: strings.Repeat("ab", 32)}}
			switch reason {
			case "format":
				r.StorageFormat = "future"
			case "migration_path":
				r.Schemas["tasks"] = []string{"unknown"}
			case "protocol":
				r.Protocol++
			case "artifact":
				r.Artifacts[0].Platform = "other"
			case "same":
				r.Version = "0.1.0"
				r.Artifacts = nil
			case "older":
				r.Version = "0.0.9"
				r.Artifacts = nil
			}
			feed, _ := signed(t, *r)
			if reason == "signature" {
				var envelope Envelope
				json.Unmarshal(feed, &envelope)
				envelope.Signature = strings.Repeat("a", 88)
				feed, _ = json.Marshal(envelope)
			}
			h := &Host{Directory: t.TempDir(), Package: Installation{Kind: "cli", Platform: "linux-amd64"}}
			if reason == "no_host" {
				h = nil
			}
			var requests atomic.Int32
			m := New(t.Context(), h, nil)
			m.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				requests.Add(1)
				if reason == "network" {
					return nil, errors.New("offline")
				}
				if req.URL.Host == "api.github.com" {
					return response([]byte(`{"tag_name":"v0.2.0"}`)), nil
				}
				return response(feed), nil
			})}
			m.check()
			s, err := m.Status()
			if err != nil {
				t.Fatal(err)
			}
			if reason == "same" || reason == "older" || reason == "network" {
				if s.Version != "" {
					t.Fatal("false update", s)
				}
				if reason == "network" && s.Discovery != "unreachable" {
					t.Fatal(s)
				}
				return
			}
			if s.Version != "0.2.0" || s.ReleaseURL != "https://github.com/jjyr/pot/releases/tag/v0.2.0" {
				t.Fatal("release hidden", s)
			}
			if s.AutoInstall != (reason == "eligible") {
				t.Fatal("incorrect eligibility", s)
			}
			if reason == "signature" && (s.Discovery != "manual" || s.Reason != "verification") {
				t.Fatal("invalid signature advertised as verified", s)
			}
			if reason != "eligible" && s.Reason == "" {
				t.Fatal("missing reason", s)
			}
		})
	}
}

func TestManualCheckIsNetworkSingleflightAndWorksWithoutInstallation(t *testing.T) {
	m := New(t.Context(), nil, nil)
	start, release := make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	m.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "api.github.com" {
			return response([]byte(`{"tag_name":"v0.2.0"}`)), nil
		}
		if count.Add(1) == 1 {
			close(start)
		}
		<-release
		return nil, errors.New("no feed")
	})}
	first := make(chan error, 1)
	go func() {
		_, err := m.Call(context.Background(), "POST", json.RawMessage(`{"action":"check","request_id":"one"}`))
		first <- err
	}()
	<-start
	firstFlight := m.checkFlight()
	for range 8 {
		if flight := m.checkFlight(); flight != firstFlight {
			t.Fatal("overlapping checks did not join the same flight")
		}
	}
	close(release)
	<-firstFlight
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatal("manual check must make exactly one feed request", count.Load())
	}
	s, err := m.Status()
	if err != nil || s.Version != "0.2.0" || s.AutoInstall {
		t.Fatal(s, err)
	}
	oldKey := buildinfo.UpdatePublicKey
	buildinfo.UpdatePublicKey = ""
	defer func() { buildinfo.UpdatePublicKey = oldKey }()
	s, _ = m.Status()
	if s.Reason != "no_key" {
		t.Fatal(s)
	}
}

func TestSignedFingerprintChangeRequiresKnownForwardLineage(t *testing.T) {
	r := eligibleRelease(t, "0.2.0")
	r.StorageFormat = StorageFormat + "-" + strings.Repeat("cd", 32)
	r.Schemas["tasks"] = append(r.Schemas["tasks"], fmt.Sprintf("%04d_forward_tasks_", len(r.Schemas["tasks"])+1)+strings.Repeat("ab", 32))
	feed, key := signed(t, *r)
	verified, err := Verify(feed, key)
	if err != nil || eligibility(verified) != "" {
		t.Fatal("supported migration hidden by fingerprint", err, eligibility(verified))
	}
	r.Schemas["tasks"][0] = "0001_changed_tasks_" + strings.Repeat("ef", 32)
	feed, key = signed(t, *r)
	verified, err = Verify(feed, key)
	if err != nil || eligibility(verified) != "migration_path" {
		t.Fatal("rewritten history accepted", err)
	}
	r = eligibleRelease(t, "0.2.0")
	r.LedgerDigest = strings.Repeat("ef", 32)
	feed, key = signed(t, *r)
	verified, err = Verify(feed, key)
	if err != nil || eligibility(verified) != "migration_path" {
		t.Fatal("changed fixed ledger accepted", err)
	}
	r = eligibleRelease(t, "0.2.0")
	r.StorageFormat = StorageFormat + "-unknown"
	if eligibility(*r) != "format" {
		t.Fatal("unknown format suffix accepted")
	}
}

func TestInstallerCrashKeepsMaintenanceAndReleaseVisible(t *testing.T) {
	directory := t.TempDir()
	host := &Host{Directory: directory}
	m := New(t.Context(), host, nil)
	s := State{Version: "0.2.0", Phase: "restarting"}
	if err := instance.WriteJSON(filepath.Join(directory, "state.json"), s); err != nil {
		t.Fatal(err)
	}
	worker, err := openLock(filepath.Join(directory, "update.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		t.Fatal(err)
	}
	m.checkWorker(s)
	if m.state().Phase != "restarting" {
		t.Fatal("live installer was declared absent")
	}
	worker.Close() // OS releases the same lock when an installer process crashes.
	m.checkWorker(s)
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "blocked" || !host.Maintaining() || status.AutoInstall || status.ReleaseURL != "https://github.com/jjyr/pot/releases/tag/v0.2.0" {
		t.Fatal(status)
	}
	if !strings.Contains(status.Error, "migration-journal.json") {
		t.Fatal("missing recovery path", status)
	}
}
