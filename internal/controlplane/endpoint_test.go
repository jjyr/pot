package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerEndpointRepairRetainsOriginalMembershipAndRejectsAnotherControl(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	control, worker, old := pair(t, nil, nil)
	original := *worker.Membership()
	workerIdentity := worker.Identity()
	replacement := peerServer(t, &Server{Directory: control})
	defer replacement.Close()
	impostor := directory(t, "other-control")
	impostor.EnableControl()
	if err := worker.SetEndpoint(ctx, replacement.URL, impostor.Identity()); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("another Control changed endpoint", err)
	}
	if *worker.Membership() != original {
		t.Fatal("rejected repair changed membership")
	}
	if err := worker.SetEndpoint(ctx, replacement.URL, control.Identity()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.Done():
	case <-ctx.Done():
		t.Fatal("old transport remained live")
	}
	updated := worker.Membership()
	if updated.Origin != replacement.URL || updated.Enrollment != original.Enrollment || worker.Identity() != workerIdentity {
		t.Fatal("repair changed original identity, epoch or credentials")
	}
	connector := Connector{Directory: worker}
	session, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
}

func TestPendingJoinEndpointRepairRetainsOriginalRequest(t *testing.T) {
	ctx := context.Background()
	control := directory(t, "control")
	control.EnableControl()
	worker := directory(t, "worker")
	invitation, _ := control.Invitation(ctx, time.Hour)
	signed, err := worker.prepareJoin("tcp://127.0.0.1:12345", control.Identity(), invitation.Code, "original-join")
	if err != nil {
		t.Fatal(err)
	}
	worker.mu.Lock()
	err = worker.update(func(b *book) error { b.Pending.Origin = ""; return nil })
	worker.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.SetEndpoint(ctx, "tcp://127.0.0.1:23456", control.Identity()); err != nil {
		t.Fatal(err)
	}
	retry, err := worker.prepareJoin("tcp://127.0.0.1:23456", control.Identity(), invitation.Code, "original-join")
	if err != nil {
		t.Fatal(err)
	}
	if string(retry.Payload) != string(signed.Payload) || retry.Signature != signed.Signature {
		t.Fatal("endpoint repair recreated uncertain enrollment")
	}
}

func TestTCPUpgradeReopensOriginalMembershipWithoutReenrollment(t *testing.T) {
	ctx := context.Background()
	control := directory(t, "control")
	control.EnableControl()
	endpoint := peerServer(t, &Server{Directory: control})
	defer endpoint.Close()
	path := filepath.Join(t.TempDir(), "runner.db")
	worker, err := Open(path, Options{RuntimeID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	connector := Connector{Directory: worker}
	invitation, _ := control.Invitation(ctx, time.Hour)
	member, err := connector.Enroll(ctx, endpoint.URL, control.Identity(), invitation.Code, "original-enrollment")
	if err != nil {
		t.Fatal(err)
	}
	originalIdentity := worker.Identity()
	worker.mu.Lock()
	var legacy map[string]any
	raw, _ := json.Marshal(worker.state)
	json.Unmarshal(raw, &legacy)
	legacy["version"] = 1
	membership := legacy["membership"].(map[string]any)
	membership["origin"] = "https://old-owner.example"
	membership["ca_pem"] = "obsolete-cert"
	raw, _ = json.Marshal(legacy)
	if _, err = worker.db.Exec(`UPDATE controlplane_state SET body=?`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.db.Exec(`DELETE FROM pot_migrations WHERE id LIKE '0002_peer_tcp_%'`); err != nil {
		t.Fatal(err)
	}
	worker.mu.Unlock()
	worker.Close()
	worker, err = Open(path, Options{RuntimeID: "worker"})
	if err != nil {
		t.Fatal("migrated membership cannot reopen", err)
	}
	defer worker.Close()
	if worker.Identity() != originalIdentity || worker.Membership().Enrollment != member.Enrollment || worker.Membership().Origin != "" {
		t.Fatal("upgrade lost original identity or authority")
	}
	connector = Connector{Directory: worker}
	if session, err := connector.Connect(ctx); err == nil {
		session.Close()
		t.Fatal("missing endpoint guessed old owner port")
	}
	if err = worker.SetEndpoint(ctx, endpoint.URL, control.Identity()); err != nil {
		t.Fatal(err)
	}
	session, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal("original membership failed after endpoint repair", err)
	}
	session.Close()
}
