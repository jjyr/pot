-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE codex_mail_consumers(id TEXT PRIMARY KEY,parent_id TEXT,active INTEGER NOT NULL DEFAULT 1);
UPDATE agent_messages SET state='interrupted',detail='codex_reader_before_consumer_fence' WHERE direction='incoming' AND state='read';
-- +migrate StatementEnd
