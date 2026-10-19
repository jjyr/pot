package tasks

import (
	"context"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestLegacyTaskMigrationPreservesAcceptedRun(t *testing.T) {
	ctx := context.Background()
	s, port, _, path := fixture(t)
	record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "migration", Text: "keep the original execution"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.DB().QueryRow("SELECT body FROM tasks WHERE id=?", record.Info.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`ALTER TABLE assistant_mail_identity RENAME COLUMN node_id TO control_id; ALTER TABLE assistant_contacts RENAME COLUMN node TO control; DROP TABLE task_input_digest_formats; DROP TABLE codex_mail_consumers; DROP TABLE pot_migrations;
CREATE TABLE task_schema(version INTEGER NOT NULL); INSERT INTO task_schema VALUES(8);
CREATE TABLE schedule_schema(version INTEGER NOT NULL); INSERT INTO schedule_schema VALUES(2);`); err != nil {
		t.Fatal(err)
	}
	options := s.options
	s.Close()
	for range 2 {
		s, err = Open(path, options)
		if err != nil {
			t.Fatal(err)
		}
		var after string
		if err := s.DB().QueryRow("SELECT body FROM tasks WHERE id=?", record.Info.ID).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("migration changed the accepted Task snapshot")
		}
		s.Close()
	}
	s, err = Open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if port.launches != 1 || port.stops != 0 {
		t.Fatal("migration replayed or stopped execution", port.launches, port.stops)
	}
	stored, err := s.Get(ctx, "assistant", record.Info.ID)
	if err != nil || stored.RunID != record.RunID || stored.Info.Phase != protocol.TaskRunning {
		t.Fatal(stored, err)
	}
}
