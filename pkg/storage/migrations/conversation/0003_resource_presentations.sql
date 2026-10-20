-- +migrate Up
-- +migrate StatementBegin
-- Resource replies have no required Markdown text. Their metadata and insertion
-- position are durable independently of tool disclosures or model replay.
CREATE INDEX ui_resource_identity ON messages(json_extract(body,'$.presentation.resource.id'))
 WHERE json_extract(body,'$.presentation.resource.id') IS NOT NULL;
-- +migrate StatementEnd
