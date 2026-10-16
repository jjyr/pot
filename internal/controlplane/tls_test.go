package controlplane

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateCATrustPersistsAndHostnameVerificationSurvivesRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	control := directory(t, "control")
	if err := control.EnableControl(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(&Server{Directory: control, Handler: func(context.Context, Principal, Request) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}})
	defer server.Close()
	path := filepath.Join(t.TempDir(), "worker.db")
	worker, err := Open(path, Options{RuntimeID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	untrusted := Connector{Directory: worker}
	if _, err = untrusted.Preview(ctx, server.URL); err == nil {
		t.Fatal("untrusted private certificate accepted")
	}
	capem := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	wrong := Connector{Directory: worker, CAPEM: capem, HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{ServerName: "wrong.invalid", MinVersion: tls.VersionTLS12}}}}
	if _, err = wrong.Preview(ctx, server.URL); err == nil {
		t.Fatal("private CA disabled hostname verification")
	}
	connector := Connector{Directory: worker, CAPEM: capem}
	identity, err := connector.Preview(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := control.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	membership, err := connector.Enroll(ctx, server.URL, identity, invitation.Code)
	if err != nil || membership.CAPEM != capem {
		t.Fatal(membership, err)
	}
	peer, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	peer.Close()
	worker.Close()
	worker, err = Open(path, Options{RuntimeID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	connector = Connector{Directory: worker}
	peer, err = connector.Connect(ctx)
	if err != nil {
		t.Fatal("saved CA not used after restart:", err)
	}
	defer peer.Close()
	result, err := peer.Call(ctx, Request{ID: "after-restart", Method: "test", Params: json.RawMessage(`{}`)})
	if err != nil || string(result) != `{"ok":true}` {
		t.Fatal(string(result), err)
	}
}
