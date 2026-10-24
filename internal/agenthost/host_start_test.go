package agenthost

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/storage"
)

func TestAgentHostRunningDaemonNeverMigrates(t *testing.T) {
	for _, version := range []string{"2", "9", hostVersion} {
		t.Run("running-version-"+version, func(t *testing.T) {
			// Bind first; never probe or send credentials to a user-owned service.
			listener, err := net.Listen("tcp", hostListen)
			if err != nil {
				t.Skip("host port already occupied")
			}
			defer listener.Close()
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			const token = "synthetic-existing-owner-token"
			legacyRunningHost(t, root, token)
			files := map[string][]byte{}
			for _, name := range []string{"control.sqlite", "tasks.sqlite", "master.key"} {
				files[name], err = os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
			}
			lock, err := os.OpenFile(filepath.Join(root, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatal(err)
			}
			defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			var authenticated atomic.Int32
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/agent-host/health" || r.Header.Get("Authorization") != "Bearer "+token {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				authenticated.Add(1)
				fmt.Fprintf(w, `{"version":%q}`, version)
			})}
			go server.Serve(listener)
			defer server.Close()
			client, err := ensureAgentHost(root)
			if version == hostVersion {
				if err != nil || client.token != token {
					t.Fatal("same-version daemon was not reused", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "port_in_use_or_version_mismatch") {
				t.Fatal("old daemon was not rejected by version", err)
			}
			if authenticated.Load() != 1 {
				t.Fatal("owner-authenticated health was not checked", authenticated.Load())
			}
			for name, before := range files {
				after, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("running daemon's schema, history, JSON or credentials changed", name, err)
				}
			}
		})
	}
}

func legacyRunningHost(t *testing.T, root, token string) {
	t.Helper()
	path := filepath.Join(root, "control.sqlite")
	store, err := secrets.Open(path, secrets.FileKey{Path: filepath.Join(root, "master.key"), Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Put(context.Background(), "existing-owner", []byte(token)); err != nil {
		t.Fatal(err)
	}
	store.Close()
	db, err := storage.OpenDatabase(path, storage.ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	var hasNode bool
	if err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('agent_adapter_host') WHERE name='node_id')").Scan(&hasNode); err != nil {
		t.Fatal(err)
	}
	if hasNode {
		if _, err = db.Exec("ALTER TABLE agent_adapter_host RENAME COLUMN node_id TO control_id"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO agent_adapter_host(singleton,control_id,owner_secret_ref,endpoint) VALUES(1,'existing-host','existing-owner','');
UPDATE pot_migrations SET id='0001_baseline_control_unsupported_development_fixture';
INSERT INTO agent_link_invitations(id,identity,endpoint,expires_at,secret_ref) VALUES('old-invite','{"address":{"control_id":"existing-host","kind":"assistant","agent_id":"codex"},"name":"Codex","adapter":"codex"}','tcp://127.0.0.1:8793',1,'old-invite-secret')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	tasks, err := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.Exec(`INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient) VALUES('old-message','{"id":"old-message","kind":"message","from":{"control_id":"peer","kind":"assistant","agent_id":"pot"},"to":{"control_id":"existing-host","kind":"assistant","agent_id":"codex"},"conversation_id":"old-conversation","text":"pending"}','incoming','read','old-conversation','codex');
INSERT INTO codex_mail_consumers(id,parent_id) VALUES('old-consumer','old-message');
INSERT INTO agent_delivery_items(batch,message) VALUES('old-consumer','old-message')`); err != nil {
		t.Fatal(err)
	}
	tasks.Close()
}
