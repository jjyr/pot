package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/systemnotifications"
	"github.com/jjyr/pot/internal/testutil"
)

func TestCommittedApprovalStreamAndOwnerRevocation(t *testing.T) {
	app := extensionApplication(t)
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 5*time.Second))
	defer cancel()
	owner, err := app.Control.Registry.Create(ctx, "lifecycle-owner", "Lifecycle owner")
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(app, "private")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/lifecycle" {
			t.Error("notification scanned business resources", r.URL.Path)
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	events, err := systemnotifications.OpenHub(filepath.Join(t.TempDir(), "notifications.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	sub, err := events.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	stream, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		events.Consume(stream, localui.NewHTTPClient(server.URL, server.Client(), "private", "local"))
	}()
	defer func() { stop(); <-done }()
	for !events.ValidateScope("local", "local", []string{owner.ID}) {
		select {
		case <-ctx.Done():
			t.Fatal("missing access frame")
		case <-time.After(time.Millisecond):
		}
	}
	if err = app.Control.Permissions.Enroll("fixture", "epoch"); err != nil {
		t.Fatal(err)
	}
	approval, stopApproval := context.WithCancel(ctx)
	defer stopApproval()
	returned := make(chan error, 1)
	go func() {
		_, err := app.Control.Permissions.Authorize(approval, control.HostRequest{Owner: owner.ID, Task: "exact-task", Turn: "turn", Generation: 1, CallID: "call", MachineID: "fixture", EnrollmentEpoch: "epoch", Tool: "exec_command", Host: true, Command: &control.CommandContext{Command: "never-expose-args", Directory: "/fixture", Shell: "/bin/sh"}, Args: json.RawMessage(`{"cmd":"never-expose-args"}`)})
		returned <- err
	}()
	defer func() { stopApproval(); <-returned }()
	record, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(record.Event)
	if record.Event.Kind != "approval" || record.Event.Owner != owner.ID || record.Event.Task != "exact-task" || strings.Contains(string(body), "never-expose-args") {
		t.Fatal(string(body))
	}
	if len(app.Control.Permissions.Pending(owner.ID)) != 1 {
		t.Fatal("notification preceded pending commit")
	}
	if err = app.Control.Registry.Delete(ctx, owner.ID, owner.Revision); err != nil {
		t.Fatal(err)
	}
	for events.Current(record.Event) {
		select {
		case <-ctx.Done():
			t.Fatal("deleted owner retained queued notification authority")
		case <-time.After(time.Millisecond):
		}
	}
}
