package tasks

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestBaselineReopenPreservesAcceptedRun(t *testing.T) {
	ctx := context.Background()
	s, port, path := fixture(t)
	record, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "migration", Text: "keep the original execution"})
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
	options := s.options
	s.db.Close()
	for range 2 {
		s, err = openTasks(t, path, options)
		if err != nil {
			t.Fatal(err)
		}
		var after string
		if err := s.DB().QueryRow("SELECT body FROM tasks WHERE id=?", record.Info.ID).Scan(&after); err != nil {
			t.Fatal(err)
		}
		var original, current Record
		if e := json.Unmarshal([]byte(before), &original); e != nil {
			t.Fatal(e)
		}
		if e := json.Unmarshal([]byte(after), &current); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(original, current) {
			t.Fatal("reopen changed the accepted Task snapshot")
		}
		s.db.Close()
	}
	s, err = openTasks(t, path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if port.launches != 1 || port.stops != 0 {
		t.Fatal("reopen replayed or stopped execution", port.launches, port.stops)
	}
	stored, err := s.Get(ctx, "assistant", record.Info.ID)
	if err != nil || stored.RunID != record.RunID || stored.Info.Phase != protocol.TaskRunning {
		t.Fatal(stored, err)
	}
}
