-- +migrate Up
ALTER TABLE run_resources RENAME TO resource_file_export;
