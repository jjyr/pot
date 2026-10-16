package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func directory(t *testing.T, id string) *Directory {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "directory.db"), Options{RuntimeID: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
func pair(t *testing.T, controlHandler, runnerHandler Handler) (*Directory, *Directory, *Peer) {
	t.Helper()
	control := directory(t, "control")
	worker := directory(t, "worker")
	if err := control.EnableControl(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(&Server{Directory: control, Handler: controlHandler})
	t.Cleanup(server.Close)
	connector := &Connector{Directory: worker, Handler: runnerHandler}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	preview, err := connector.Preview(ctx, server.URL)
	if err != nil || preview != control.Identity() {
		t.Fatalf("preview: %v", err)
	}
	invitation, err := control.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m, err := connector.Enroll(ctx, server.URL, preview, invitation.Code)
	if err != nil {
		t.Fatal(err)
	}
	if m.Enrollment.Token == control.OwnerCredential() {
		t.Fatal("credential roles mixed")
	}
	p, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	waitPeer(t, control, "worker")
	return control, worker, p
}
func waitPeer(t *testing.T, d *Directory, id string) *Peer {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p, err := d.Peer(id); err == nil {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("machine did not connect")
	return nil
}
func TestOutwardBidirectionalSignedRPCAndRevocation(t *testing.T) {
	callback := func(ctx context.Context, p Principal, r Request) (json.RawMessage, error) {
		if p.RuntimeID != "worker" || p.Epoch != 1 || p.Owner {
			t.Error("wrong authenticated Runner principal")
		}
		return json.RawMessage(`{"callback":true}`), nil
	}
	execution := func(ctx context.Context, p Principal, r Request) (json.RawMessage, error) {
		if p.RuntimeID != "control" || p.Epoch != 1 || p.Owner {
			t.Error("wrong authenticated Control principal")
		}
		return r.Params, nil
	}
	control, worker, peer := pair(t, callback, execution)
	ctx := context.Background()
	remote := waitPeer(t, control, "worker")
	result, err := remote.Call(ctx, Request{"launch-1", "test.execute", json.RawMessage(`{"hello":"world"}`)})
	if err != nil || string(result) != `{"hello":"world"}` {
		t.Fatalf("execution: %s %v", result, err)
	}
	result, err = peer.Call(ctx, Request{"model-1", "test.model", json.RawMessage(`{}`)})
	if err != nil || string(result) != `{"callback":true}` {
		t.Fatalf("callback: %s %v", result, err)
	}
	if err = control.SetLabels(ctx, "worker", []string{}); err != nil {
		t.Fatal(err)
	}
	peer.Close()
	connector := Connector{Directory: worker, Handler: execution}
	second, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	waitPeer(t, control, "worker")
	machines, _ := control.Machines(ctx)
	if len(machines) != 1 || machines[0].Labels == nil || len(machines[0].Labels) != 0 {
		t.Fatalf("reconnect reset labels: %+v", machines)
	}
	if err = control.Remove(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.Done():
	case <-time.After(time.Second):
		t.Fatal("revocation did not close session")
	}
	if _, err = connector.Connect(ctx); err == nil {
		t.Fatal("revoked credential reconnected")
	}
}
func TestInvitationRetryAndDurableIdentity(t *testing.T) {
	ctx := context.Background()
	control := directory(t, "control")
	worker := directory(t, "worker")
	control.EnableControl()
	inv, _ := control.Invitation(ctx, time.Hour)
	signed, err := worker.prepareJoin("http://localhost:12345", control.Identity(), inv.Code)
	if err != nil {
		t.Fatal(err)
	}
	first, err := control.Join(ctx, signed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := control.Join(ctx, signed)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Payload) != string(second.Payload) || first.Signature != second.Signature {
		t.Fatal("uncertain join retry changed credential")
	}
	var e Enrollment
	if err = verify(control.Identity(), first, &e); err != nil {
		t.Fatal(err)
	}
	var request JoinRequest
	core.DecodeJSON(signed.Payload, &request)
	request.RequestID = "other"
	altered, _ := sign(worker.key, request)
	if _, err = control.Join(ctx, altered); err == nil {
		t.Fatal("consumed invitation accepted different request")
	}
	path := filepath.Join(t.TempDir(), "runner.db")
	d, err := Open(path, Options{RuntimeID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	id := d.Identity()
	if err = d.SaveMembership(ctx, Membership{Origin: "http://localhost:12345", Enrollment: e}); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path, Options{RuntimeID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Identity() != id || d.Role() != "runner" || d.Membership().Enrollment.Token != e.Token {
		t.Fatal("identity/membership not durable")
	}
	if err = d.EnableControl(); err == nil {
		t.Fatal("Runner became Control")
	}
}
func TestPinnedJoinAndOwnerMemberSeparation(t *testing.T) {
	control := directory(t, "control")
	worker := directory(t, "worker")
	control.EnableControl()
	var ownerCalls atomic.Int32
	server := httptest.NewServer(&Server{Directory: control, OwnerHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ownerCalls.Add(1); w.WriteHeader(200) })})
	defer server.Close()
	ctx := context.Background()
	inv, _ := control.Invitation(ctx, time.Hour)
	connector := Connector{Directory: worker}
	wrong := control.Identity()
	wrong.Fingerprint = "wrong"
	if _, err := connector.Enroll(ctx, server.URL, wrong, inv.Code); err == nil {
		t.Fatal("invalid fingerprint accepted")
	}
	member, err := connector.Enroll(ctx, server.URL, control.Identity(), inv.Code)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		token, origin string
		status        int
	}{{member.Enrollment.Token, "", 403}, {control.OwnerCredential(), "https://evil.example", 403}, {control.OwnerCredential(), "", 200}} {
		request, _ := http.NewRequest("GET", server.URL+"/control/v1/owner/test", nil)
		request.Header.Set("Authorization", "Bearer "+item.token)
		request.Header.Set("Origin", item.origin)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != item.status {
			t.Fatalf("role/Origin status %d", response.StatusCode)
		}
	}
	if ownerCalls.Load() != 1 {
		t.Fatal("owner route leaked")
	}
}
func TestDroppedReplyDoesNotResend(t *testing.T) {
	var count atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	handler := func(ctx context.Context, p Principal, r Request) (json.RawMessage, error) {
		count.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return json.RawMessage(`null`), nil
	}
	control, _, _ := pair(t, nil, handler)
	peer := waitPeer(t, control, "worker")
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := peer.Call(ctx, Request{"effect-1", "test.effect", json.RawMessage(`{}`)})
		result <- err
	}()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, ErrUncertain) {
		t.Fatalf("uncertainty missing: %v", err)
	}
	if count.Load() != 1 {
		t.Fatal("effect resubmitted")
	}
}
func TestSignedAudienceAndEpoch(t *testing.T) {
	control, worker, peer := pair(t, nil, nil)
	remote := waitPeer(t, control, "worker")
	message := wire{Version: 1, Sender: worker.Identity().RuntimeID, Audience: control.Identity().RuntimeID, Epoch: 2, Kind: "request", ID: "bad", Method: "test.bad", Params: json.RawMessage(`{}`)}
	signed, _ := sign(worker.key, message)
	raw, _ := json.Marshal(signed)
	if err := peer.conn.Write(context.Background(), 1, raw); err != nil {
		t.Fatal(err)
	}
	select {
	case <-remote.Done():
	case <-time.After(time.Second):
		t.Fatal("wrong membership epoch accepted")
	}
}
func TestEmbeddedCannotServeOrJoin(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "app.db"), Options{RuntimeID: "app", Embedded: true})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.EnableControl() == nil {
		t.Fatal("embedded Control allowed")
	}
	request := httptest.NewRequest("GET", "http://localhost/control/v1/identity", nil)
	recorder := httptest.NewRecorder()
	(&Server{Directory: d}).ServeHTTP(recorder, request)
	if recorder.Code != 403 {
		t.Fatal("embedded listener exposed")
	}
}
