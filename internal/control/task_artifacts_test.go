package control

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestTaskArtifactCollection(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []string{"absent", "empty", "nested", "symlinks", "directory-link", "oversized", "too-many"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			artifacts := filepath.Join(dir, taskArtifactsDirectory)
			if scenario != "absent" && scenario != "directory-link" {
				if err := os.Mkdir(artifacts, 0700); err != nil {
					t.Fatal(err)
				}
			}
			var want []string
			wantError := false
			switch scenario {
			case "nested", "symlinks":
				writeTaskFixture(t, filepath.Join(artifacts, "nested", "报告.md"), "findings")
				writeTaskFixture(t, filepath.Join(dir, "working.txt"), "not a deliverable")
				want = []string{".pot-artifacts/nested/报告.md"}
				if scenario == "symlinks" {
					outside := filepath.Join(t.TempDir(), "private")
					writeTaskFixture(t, outside, "private")
					if err := os.Symlink(outside, filepath.Join(artifacts, "outside")); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("nested", filepath.Join(artifacts, "inside")); err != nil {
						t.Fatal(err)
					}
					wantError = true
				}
			case "directory-link":
				if err := os.Symlink(t.TempDir(), artifacts); err != nil {
					t.Fatal(err)
				}
				wantError = true
			case "oversized":
				file, err := os.Create(filepath.Join(artifacts, "huge"))
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(taskArtifactsSizeLimit + 1); err != nil {
					t.Fatal(err)
				}
				file.Close()
				wantError = true
			case "too-many":
				for i := 0; i <= taskArtifactsEntryLimit; i++ {
					writeTaskFixture(t, filepath.Join(artifacts, fmt.Sprintf("%03d.txt", i)), "")
				}
				wantError = true
			}
			files, err := collectTaskArtifacts(ctx, root)
			if (err != nil) != wantError {
				t.Fatal(files, err)
			}
			if scenario == "too-many" {
				if len(files) > taskArtifactsFileLimit {
					t.Fatal(files)
				}
				return
			}
			if !reflect.DeepEqual(files, want) {
				t.Fatal(files, want)
			}
		})
	}
}

func TestTaskArtifactErrorsPreserveFinalReplyAndStatus(t *testing.T) {
	ctx := context.Background()
	s := taskResourceService(t)
	root := filepath.Join(s.options.Workspace, "tasks", "worker")
	writeTaskFixture(t, filepath.Join(root, ".pot-artifacts", "report.txt"), "report")
	bindTestWorkspace(t, s, "worker", root)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".pot-artifacts", "escape")); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []protocol.TaskPhase{protocol.TaskComplete, protocol.TaskFailed, protocol.TaskInterrupted, protocol.TaskCancelled} {
		task := tasks.Record{Info: protocol.TaskInfo{ID: "worker"}, Origin: protocol.TaskOrigin{Kind: "manual"}, Launch: &tasks.Launch{ExecutionTarget: core.RuntimeTarget(s.options.RuntimeID)}}
		event := protocol.TaskResultEvent{Phase: phase, Text: "Final reply with evidence and limitations", Error: "original execution diagnostic"}
		commit, err := s.prepareTaskOutcome(ctx, task, event)
		if err != nil {
			t.Fatal(err)
		}
		if err := storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error { return commit(ctx, tx, &event) }); err != nil {
			t.Fatal(err)
		}
		if event.Phase != phase || event.Text != "Final reply with evidence and limitations" || event.Error != "original execution diagnostic" || event.ArtifactsError == "" || !reflect.DeepEqual(event.Artifacts, []string{".pot-artifacts/report.txt"}) {
			t.Fatal(event)
		}
	}
}
