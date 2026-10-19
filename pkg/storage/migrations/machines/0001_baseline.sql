-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE IF NOT EXISTS controlplane_state(id INTEGER PRIMARY KEY CHECK(id=1),body TEXT NOT NULL)
-- +migrate StatementEnd
