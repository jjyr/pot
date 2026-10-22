package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ImportLocalRuns moves original run evidence into the Task database once. The
// retired file is never a live execution store after this receipt commits.
func ImportLocalRuns(root string) error {
	target, err := OpenDatabase(filepath.Join(root, "tasks.sqlite"), TasksDatabase)
	if err != nil {
		return err
	}
	defer target.Close()
	var pending int
	if err = target.QueryRow("SELECT count(*) FROM journal_file_import").Scan(&pending); err != nil || pending == 0 {
		return err
	}
	path := filepath.Join(root, "runner.sqlite")
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		_, err = target.Exec("DELETE FROM journal_file_import")
		return err
	} else if err != nil {
		return err
	}
	source, err := OpenDatabase(path, RunnerDatabase)
	if err != nil {
		return err
	}
	defer source.Close()
	if err = ExportResourceFiles(source, filepath.Join(root, "cache", "resources")); err != nil {
		return err
	}
	return Transaction(context.Background(), target, func(tx *sql.Tx) error {
		for _, table := range []string{"run_workspaces", "task_runs", "task_events", "retired_execution_receipts"} {
			rows, err := source.Query("SELECT * FROM " + table)
			if err != nil {
				return err
			}
			cols, err := rows.Columns()
			if err != nil {
				rows.Close()
				return err
			}
			query := "INSERT INTO " + table + " VALUES(" + strings.TrimRight(strings.Repeat("?,", len(cols)), ",") + ")"
			for rows.Next() {
				values := make([]any, len(cols))
				refs := make([]any, len(cols))
				for i := range values {
					refs[i] = &values[i]
				}
				if err = rows.Scan(refs...); err != nil {
					rows.Close()
					return err
				}
				if _, err = tx.Exec(query, values...); err != nil {
					rows.Close()
					return err
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		_, err := tx.Exec("DELETE FROM journal_file_import")
		return err
	})
}
