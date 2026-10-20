-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE compaction_evidence(sequence INTEGER PRIMARY KEY AUTOINCREMENT,compaction_id TEXT NOT NULL REFERENCES compactions(id),at INTEGER NOT NULL,body TEXT NOT NULL);
CREATE INDEX compaction_evidence_operation ON compaction_evidence(compaction_id,sequence);
-- +migrate StatementEnd
