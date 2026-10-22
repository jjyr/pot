package storage

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func nodeMigrationPrefix(t *testing.T, db *sql.DB, kind Database, version int) {
	t.Helper()
	files := fstest.MapFS{}
	err := fs.WalkDir(migrationFiles, "migrations/"+string(kind), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		var v int
		_, _ = fmt.Sscanf(d.Name(), "%04d_", &v)
		if v <= version {
			data, e := migrationFiles.ReadFile(path)
			if e != nil {
				return e
			}
			files[path] = &fstest.MapFile{Data: data}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateDatabase(context.Background(), db, kind, files); err != nil {
		t.Fatal(err)
	}
}
func TestNodeIdentityTasksUpgradePreservesReceiptsAndCorrespondence(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	nodeMigrationPrefix(t, db, TasksDatabase, 5)
	old := `{"kind":"message","id":"original-message","from":{"control_id":"remote","kind":"assistant","agent_id":"alice"},"to":{"control_id":"local","kind":"assistant","agent_id":"main"},"conversation_id":"00000000-0000-4000-8000-000000000001","reply_to":null,"text":"literal control_id and node_id remain text","hops":0}`
	var before protocol.Envelope
	expectedRaw := strings.ReplaceAll(old, `"control_id":`, `"node_id":`)
	if err := json.Unmarshal([]byte(expectedRaw), &before); err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	oldRef := func(node, agent string) map[string]string {
		return map[string]string{"control_id": node, "kind": "assistant", "agent_id": agent}
	}
	oldKey, _ := core.HashJSON([]any{oldRef("local", "main"), oldRef("remote", "alice")})
	newKey, _ := core.HashJSON([]protocol.AgentRef{before.To, before.From})
	_, err = db.Exec(`INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,detail)VALUES('original-message',?,'in','waiting','wire-conversation','main','immutable-receipt');
INSERT INTO agent_delivery_items VALUES('original-message','original-message');

INSERT INTO assistant_mail_identity VALUES(1,'local');
INSERT INTO assistant_contacts VALUES('remote','{"control_id":"remote","assistants":[{"address":{"control_id":"remote","kind":"assistant","agent_id":"alice"},"name":"Alice"}],"tasks":[]}');
INSERT INTO task_authority VALUES('actual-control','runner');
INSERT INTO tasks VALUES('main','task','running',10,'{"schema_version":9,"info":{"address":{"control_id":"local","kind":"task","agent_id":"task"}},"request":{"text":"original work"},"launch":{"control_id":"actual-control"},"run_id":"original-run","launch_sent":true}');
INSERT INTO plugin_background VALUES('fixture',1,'main','{"control_id":"config","kind":"assistant","agent_id":"business"}',1,'{"id":"plugin-state","public_key":"ordinary"}',10,'unchanged','conversation');`, old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO assistant_mail_sessions VALUES('main',?,'{}','{"type":"context","version":2,"pending":[],"messages":[{"text":"original history"}]}')`, oldKey); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var raw, state, detail, session, control, runtime, config, checkpoint string
	if err = db.QueryRow("SELECT envelope,state,detail FROM agent_messages WHERE id='original-message'").Scan(&raw, &state, &detail); err != nil {
		t.Fatal(err)
	}
	if raw != string(expected) || state != "waiting" || detail != "immutable-receipt" {
		t.Fatal("message or receipt changed", raw, state, detail)
	}
	if err = db.QueryRow("SELECT conversation FROM assistant_mail_sessions WHERE assistant='main'").Scan(&session); err != nil || session != newKey {
		t.Fatal("peer context lost its migrated route", session, err)
	}
	if err = db.QueryRow("SELECT control_id,runtime_id FROM retired_task_authority").Scan(&control, &runtime); err != nil || control != "actual-control" || runtime != "runner" {
		t.Fatal(control, runtime, err)
	}
	if err = db.QueryRow("SELECT body FROM tasks").Scan(&raw); err != nil || !strings.Contains(raw, `"schema_version":11`) || !strings.Contains(raw, `"node_id":"local"`) || !strings.Contains(raw, `"control_id":"actual-control"`) || !strings.Contains(raw, `"run_id":"original-run"`) {
		t.Fatal(raw, err)
	}
	if err = db.QueryRow("SELECT config,state FROM plugin_background").Scan(&config, &checkpoint); err != nil || config != `{"control_id":"config","kind":"assistant","agent_id":"business"}` || checkpoint != `{"id":"plugin-state","public_key":"ordinary"}` {
		t.Fatal("opaque plugin state changed", config, checkpoint, err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM agent_delivery_items WHERE batch='original-message' AND message='original-message'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = db.QueryRow("SELECT directory FROM assistant_contacts WHERE node='remote'").Scan(&raw); err != nil || strings.Contains(raw, `"control_id"`) {
		t.Fatal(raw, err)
	}
}

type nodeMigrationKeys struct {
	key  []byte
	fail bool
}

func (k nodeMigrationKeys) Key(context.Context, bool) ([]byte, error) {
	if k.fail {
		return nil, fmt.Errorf("existing key unavailable")
	}
	return append([]byte(nil), k.key...), nil
}
func TestNodeIdentityControlUpgradePreservesEncryptedPairAndClaim(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	nodeMigrationPrefix(t, db, ControlDatabase, 8)
	key := make([]byte, 32)
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	local := `{"address":{"control_id":"local","kind":"assistant","agent_id":"main"},"name":"Local","adapter":"pot"}`
	remote := `{"address":{"control_id":"remote","kind":"assistant","agent_id":"codex"},"name":"Codex","adapter":"codex"}`
	host := `{"id":"remote","public_key":"stable-peer-public-key"}`
	metadata := `{"schema_version":2,"peer_host":` + host + `,"id":"stable-connection","local":` + local + `,"peer":` + remote + `,"endpoint":"tcp://100.88.0.2:8793","state":"connecting","created_at":"2026-10-19T00:00:00Z","updated_at":"2026-10-19T00:00:00Z"}`
	pair := `{"id":"stable-connection","invitation":"original-invitation","local":` + local + `,"peer":` + remote + `,"code":"original-capability"}`
	// A receiving-side claim reverses local/peer, retaining the authenticated host.
	receiving := strings.Replace(metadata, `"local":`+local+`,"peer":`+remote, `"local":`+remote+`,"peer":`+local, 1)
	receiving = strings.Replace(receiving, host, `{"id":"local","public_key":"stable-local-public-key"}`, 1)
	oldClaim := sha256.Sum256([]byte("[" + pair + `,{"id":"local","public_key":"stable-local-public-key"}]`))
	_, err := db.Exec(`INSERT INTO agent_link_connections VALUES('stable-connection','old-route','connecting',?,'pair-secret','stable-peer-public-key')`, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO agent_link_connections VALUES('receiving','receiver-route','ready',?,'receiver-secret','stable-local-public-key')`, receiving); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO agent_link_invitations VALUES('original-invitation',?,'tcp://100.88.0.2:8793',9999999999999,'invite-secret',?,'receiving')`, remote, hex.EncodeToString(oldClaim[:])); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO secrets VALUES('pair-secret',X'',X'',X''),('receiver-secret',X'',X'',X''),('invite-secret',X'',X'',X''),('unrelated-provider',X'01',X'02',X'03');
INSERT INTO agent_link_connections VALUES('revoked-original','retired-route','revoked','{"schema_version":2,"id":"revoked-original","state":"revoked","revocation_pending":false}','already-deleted-credential','old-public-key');
INSERT INTO codex_agent_host VALUES(1,'stable-codex-node','owner-secret','tcp://100.88.0.3:8793','stable-public-key','host-key');`); err != nil {
		t.Fatal(err)
	}
	if err = Transaction(ctx, db, func(tx *sql.Tx) error {
		for ref, value := range map[string]string{"pair-secret": `{"pair":` + pair + `}`, "receiver-secret": "{}", "invite-secret": "original-capability"} {
			if e := writeNodeSecret(ctx, tx, aead, ref, []byte(value)); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(ctx, db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	if err = FinalizeNodeIdentity(ctx, db, nodeMigrationKeys{fail: true}); err == nil {
		t.Fatal("missing key silently completed conversion")
	}
	var pending int
	if err = db.QueryRow("SELECT COUNT(*) FROM node_identity_secret_migration").Scan(&pending); err != nil || pending != 1 {
		t.Fatal("failed conversion lost its receipt", pending, err)
	}
	for range 2 {
		if err = Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
		if err = FinalizeNodeIdentity(ctx, db, nodeMigrationKeys{key: key}); err != nil {
			t.Fatal(err)
		}
	}
	var raw, route, ref, claim, codexID, pub, hostKey string
	if err = db.QueryRow("SELECT metadata,route,secret_ref FROM agent_link_connections WHERE id='stable-connection'").Scan(&raw, &route, &ref); err != nil || strings.Contains(raw, `"control_id"`) || strings.Contains(raw, `"id":"remote"`) || !strings.Contains(raw, `"schema_version":3`) || ref != "pair-secret" {
		t.Fatal(raw, route, ref, err)
	}
	var expectedRoute string
	if err = db.QueryRow("SELECT pot_node_route(?)", raw).Scan(&expectedRoute); err != nil || route != expectedRoute || route == "old-route" {
		t.Fatal(route, expectedRoute, err)
	}
	if err = Transaction(ctx, db, func(tx *sql.Tx) error {
		raw, e := readNodeSecret(ctx, tx, aead, "pair-secret")
		if e != nil {
			return e
		}
		if strings.Contains(string(raw), `"control_id"`) || !strings.Contains(string(raw), `"code":"original-capability"`) {
			return fmt.Errorf("lost original pair: %s", raw)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT claim_hash FROM agent_link_invitations").Scan(&claim); err != nil || claim != hex.EncodeToString(oldClaim[:]) {
		t.Fatal("original claim hash changed", claim, err)
	}
	var hashFormat int
	if err = db.QueryRow("SELECT format FROM agent_link_claim_digest_formats WHERE invitation='original-invitation'").Scan(&hashFormat); err != nil || hashFormat != 1 {
		t.Fatal(hashFormat, err)
	}
	if err = db.QueryRow("SELECT node_id,peer_public_key,peer_secret_ref FROM codex_agent_host").Scan(&codexID, &pub, &hostKey); err != nil || codexID != "stable-codex-node" || pub != "stable-public-key" || hostKey != "host-key" {
		t.Fatal(codexID, pub, hostKey, err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM secrets WHERE id='unrelated-provider' AND ciphertext=X'02'").Scan(&count); err != nil || count != 1 {
		t.Fatal("provider key altered", count, err)
	}
}

func TestNodeMigrationTypedBoundariesAndPendingEffects(t *testing.T) {
	opaque := `{"control_id":"business","kind":"assistant","agent_id":"ordinary","nested":{"id":"business","public_key":"opaque"},"schema_version":99,"info":{},"request":{},"run_id":"ordinary"}`
	for _, mode := range []string{"operation", "checkpoint", "run"} {
		out, err := migrateNodeJSON([]byte(opaque), mode)
		if err != nil || string(out) != opaque {
			t.Fatal(mode, string(out), err)
		}
	}
	spaced := `{"kind":"plugin_event","from":{"control_id":"local","kind":"runtime","agent_id":"plugin-x"},"to":{"control_id":"local","kind":"assistant","agent_id":"main"},"plugin": { "event": { "body":"\u4e2d", "custom": { "control_id": "business", "kind":"assistant", "agent_id":"payload" } } } }`
	converted, err := migrateNodeJSON([]byte(spaced), "envelope")
	if err != nil || !strings.Contains(string(converted), `"plugin":{ "event": { "body":"\u4e2d", "custom": { "control_id": "business", "kind":"assistant", "agent_id":"payload" } } }`) {
		t.Fatal("opaque subtree bytes changed", string(converted), err)
	}

	operation := `{"tool":"custom_tool","args":` + opaque + `,"result":` + opaque + `}`
	out, err := migrateNodeJSON([]byte(operation), "operation")
	if err != nil || string(out) != operation {
		t.Fatal(string(out), err)
	}
	checkpoint := `{"type":"context","version":2,"pending":[{"id":"effect","name":"send_message","arguments":{"to":{"control_id":"remote","kind":"assistant","agent_id":"alice"},"text":"control_id"}}],"started":"effect","receipts":[],"messages":[{"role":"tool","text":"historical control_id","replay":` + opaque + `}]}`
	out, err = migrateNodeJSON([]byte(checkpoint), "checkpoint")
	if err != nil || !strings.Contains(string(out), `"node_id":"remote"`) || !strings.Contains(string(out), `"started":"effect"`) || !strings.Contains(string(out), opaque) {
		t.Fatal(string(out), err)
	}
	// Unknown operation admission stays unknown; migrating a typed message address
	// changes its input binding atomically without admitting any new execution.
	db := migrationDB(t)
	nodeMigrationPrefix(t, db, OperationsDatabase, 1)
	raw := `{"owner":"main","turn":"original-turn","generation":1,"call_id":"effect","target":{"kind":"runtime","runtime_id":"runner"},"tool":"send_message","args":{"to":{"control_id":"remote","kind":"assistant","agent_id":"alice"},"text":"hello"},"worker":false}`
	hash, _ := core.HashJSON(json.RawMessage(raw))
	_, err = db.Exec("INSERT INTO operations VALUES('stable-id',?,'unknown',NULL,'original uncertainty')", hash)
	if err == nil {
		_, err = db.Exec("INSERT INTO operation_inputs VALUES('stable-id','main','original-turn',?)", raw)
	}
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(context.Background(), db, OperationsDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var state, changed, body string
	if err = db.QueryRow("SELECT state,input_hash,body FROM operations JOIN operation_inputs USING(id)").Scan(&state, &changed, &body); err != nil || state != "unknown" || !strings.Contains(body, `"node_id":"remote"`) {
		t.Fatal(state, body, err)
	}
	expected, _ := core.HashJSON(json.RawMessage(body))
	if changed != expected || changed == hash {
		t.Fatal("operation digest lost its typed content binding", changed, expected)
	}
}
