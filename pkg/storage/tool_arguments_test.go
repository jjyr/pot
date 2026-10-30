package storage

import "testing"

func TestToolArgumentsUpgradePreservesUnknownHistory(t *testing.T) {
	db := migrationDB(t)
	ctx := t.Context()
	if err := migrateDatabase(ctx, db, UsageDatabase, initialSchema(t, UsageDatabase)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tools(id,scope,turn_id,name,at,bytes) VALUES('old','owner','turn','exec',10,17)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, db, UsageDatabase); err != nil {
			t.Fatal(err)
		}
		var unknown bool
		var agent string
		var size int
		if err := db.QueryRow(`SELECT argument_bytes IS NULL,agent,bytes FROM tools WHERE id='old'`).Scan(&unknown, &agent, &size); err != nil || !unknown || agent != "unknown" || size != 17 {
			t.Fatal(unknown, agent, size, err)
		}
	}
	if _, err := db.Exec(`UPDATE tools SET argument_bytes=-1 WHERE id='old'`); err == nil {
		t.Fatal("negative bytes accepted")
	}
}

func TestCommunicationUsageIndexUpgradePreservesMessages(t *testing.T) {
	db := migrationDB(t)
	ctx := t.Context()
	if err := migrateDatabase(ctx, db, TasksDatabase, initialSchema(t, TasksDatabase)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,accepted_at_ms) VALUES('message','{}','in','queued','conversation','owner',123)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM agent_messages INDEXED BY agent_messages_usage_time WHERE accepted_at_ms=123 AND id='message'`).Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
	}
}
