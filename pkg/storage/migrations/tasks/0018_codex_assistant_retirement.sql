-- +migrate Up
ALTER TABLE codex_link_assistants ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN (0,1));
