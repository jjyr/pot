-- +migrate Up
-- +migrate StatementBegin
UPDATE providers SET metadata=json_set(metadata,'$.type_id',
 'standard-model-providers/custom') WHERE json_extract(metadata,'$.type_id') IS NULL
 AND json_type(metadata,'$.endpoint')='text' AND json_type(metadata,'$.protocol')='text';
CREATE TABLE provider_metadata(
 route_id TEXT NOT NULL REFERENCES providers(id),
 method TEXT NOT NULL CHECK(method IN ('list_models','read_allowances')),
 source_digest TEXT NOT NULL,
 payload TEXT NOT NULL CHECK(json_valid(payload)),
 PRIMARY KEY(route_id,method)
);
-- +migrate StatementEnd
