-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE codex_link_assistants ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE codex_link_assistants ADD COLUMN character TEXT NOT NULL DEFAULT '';
ALTER TABLE codex_link_assistants ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
UPDATE codex_link_assistants SET character=pot_avatar_character(name,id);
UPDATE codex_link_assistants SET avatar=pot_character_avatar(character,id);
-- +migrate StatementEnd
