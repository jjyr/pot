package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestTaskNodeIdentitySurvivesReopenAndRejectsAnotherNode(t *testing.T) {
	s, _, path := fixture(t)
	ctx := context.Background()
	r, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "original", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	options := s.options
	s.db.Close()
	foreign := options
	foreign.NodeID = "another-node"
	if other, err := openTasks(t, path, foreign); err == nil {
		other.db.Close()
		t.Fatal("opened another node's task store")
	}
	for range 2 {
		s, err = openTasks(t, path, options)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := s.Get(ctx, "assistant", r.Info.ID)
		if err != nil || s.NodeID() != "machine" || stored.RunID != r.RunID || stored.NodeID != "machine" {
			t.Fatal("task ownership changed", stored, err)
		}
		s.db.Close()
	}
}

func TestPreparedTaskCannotEnqueueOnAnotherNode(t *testing.T) {
	s, executor, _ := fixture(t)
	ctx := context.Background()
	request := protocol.CreateTask{RequestID: "remote", Target: &protocol.AgentRef{NodeID: "another-node", Kind: "assistant", AgentID: "assistant"}, Text: "work"}
	s.options.ResolveModel = func(context.Context, string, protocol.CreateTask) (Selection, error) {
		t.Fatal("remote target reached local model selection")
		return Selection{}, nil
	}
	if _, err := s.AcceptLocal(ctx, "assistant", request); err == nil {
		t.Fatal("remote target reached local admission")
	}
	err := storage.Transaction(ctx, s.DB(), func(tx *sql.Tx) error {
		_, err := s.EnqueueTx(ctx, tx, "assistant", request, selection(t), protocol.TaskOrigin{Kind: "manual"}, 1)
		return err
	})
	if err == nil {
		t.Fatal("prepared configuration bypassed local task admission")
	}
	for _, table := range []string{"tasks", "task_writes", "agent_messages"} {
		var count int
		if err := s.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("rejected task left durable work", table, count, err)
		}
	}
	if executor.launches != 0 {
		t.Fatal("rejected task executed")
	}
}

func TestHistoricalRemoteTaskCannotUseLocalExecutorAfterRestart(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "accepted"}[accepted], func(t *testing.T) {
			s, executor, path := fixture(t)
			ctx := context.Background()
			request := protocol.CreateTask{RequestID: "original", Text: "work"}
			r, err := s.AcceptLocal(ctx, "assistant", request)
			if err != nil {
				t.Fatal(err)
			}
			if accepted {
				if err := s.Tick(ctx); err != nil {
					t.Fatal(err)
				}
				stored, err := s.Get(ctx, "assistant", r.Info.ID)
				if err != nil {
					t.Fatal(err)
				}
				r = *stored
			}
			// Keep an identically addressed local execution in the fake executor.
			// A retired remote record must never inspect, stop or resume that run.
			r.NodeID, r.Info.MachineID = "retired-runner", "retired-runner"
			target := core.RuntimeTarget("retired-runner")
			r.Info.ExecutionTarget = &target
			if r.Launch != nil {
				r.Launch.RuntimeID, r.Launch.ExecutionTarget = "retired-runner", target
			}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB().Exec("UPDATE tasks SET body=? WHERE id=?", string(raw), r.Info.ID); err != nil {
				t.Fatal(err)
			}
			options := s.options
			s.db.Close()
			s, err = openTasks(t, path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer s.db.Close()
			for range 2 {
				if err := s.Tick(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if accepted {
				if err := s.Control(ctx, "assistant", r.Info.ID, r.RunID, "pause"); err == nil {
					t.Fatal("controlled retired execution")
				}
				if err := s.ResumeWaiting(ctx, "assistant", r.Info.ID); err == nil {
					t.Fatal("resumed retired execution")
				}
			}
			if local, err := s.LocalActive(ctx); err != nil || len(local) != 0 {
				t.Fatal("historical execution counted as local", local, err)
			}
			if err := s.Stop(ctx, "assistant", r.Info.ID, "cancel"); err != nil {
				t.Fatal(err)
			}
			retained, err := s.AcceptLocal(ctx, "assistant", request)
			if err != nil || retained.RunID != r.RunID || retained.NodeID != "retired-runner" || retained.Info.Phase != protocol.TaskWaiting {
				t.Fatal("historical retry changed original destination or outcome", retained, err)
			}
			wantLaunches := 0
			if accepted {
				wantLaunches = 1
			}
			if executor.launches != wantLaunches || executor.inspections != 0 || executor.stops != 0 {
				t.Fatal("historical task reached the local executor", executor)
			}
		})
	}
}

func TestTaskAdmissionAndRunCreationRollbackTogether(t *testing.T) {
	s, _, path := fixture(t)
	ctx := context.Background()
	r, err := s.AcceptLocal(ctx, "assistant", protocol.CreateTask{RequestID: "atomic", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := openJournal(t, path, "machine", "boot")
	if err != nil {
		t.Fatal(err)
	}
	defer journal.db.Close()
	s.options.Executor = &Executor{Journal: journal}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_admission BEFORE UPDATE ON tasks WHEN json_extract(NEW.body,'$.launch') IS NOT NULL BEGIN SELECT RAISE(ABORT,'admission write failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Tick(ctx); err == nil {
		t.Fatal("accepted failed admission")
	}
	stored, err := s.Get(ctx, "assistant", r.Info.ID)
	if err != nil || stored.Launch != nil {
		t.Fatal("partial task admission", stored, err)
	}
	run, err := journal.Inspect(ctx, "machine", r.Info.ID, r.RunID)
	if err != nil || run != nil {
		t.Fatal("orphan run after rollback", run, err)
	}
}
