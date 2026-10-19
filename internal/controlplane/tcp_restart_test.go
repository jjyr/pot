package controlplane

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestTCPPeerIdentityPersistsAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	control := directory(t, "control")
	if err := control.EnableControl(); err != nil {
		t.Fatal(err)
	}
	server := peerServer(t, &Server{Directory: control, Handler: func(context.Context, Principal, Request) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}})
	defer server.Close()
	path := filepath.Join(t.TempDir(), "worker.db")
	worker, err := Open(path, Options{RuntimeID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	identity := worker.Identity()
	connector := Connector{Directory: worker}
	preview, err := connector.Preview(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := control.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	member, err := connector.Enroll(ctx, server.URL, preview, invitation.Code)
	if err != nil {
		t.Fatal(err)
	}
	session, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	worker.Close()
	worker, err = Open(path, Options{RuntimeID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	if worker.Identity() != identity || worker.Membership().Enrollment != member.Enrollment {
		t.Fatal("restart changed pinned identity or original authority")
	}
	connector = Connector{Directory: worker}
	session, err = connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.Call(ctx, Request{ID: "after-restart", Method: "test", Params: json.RawMessage(`{}`)})
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatal(string(result), err)
	}
}
