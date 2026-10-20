-- +migrate Up
-- Keep the original stable host key while retiring membership authority.
-- Existing execution journals and retired membership facts remain untouched.
-- +migrate StatementBegin
CREATE TABLE node_identity(id INTEGER PRIMARY KEY CHECK(id=1),body TEXT NOT NULL)
-- +migrate StatementEnd
INSERT INTO node_identity(id,body)
SELECT id,json_object('version',1,'runtime_id',json_extract(body,'$.runtime_id'),
  'key',json_extract(body,'$.key'),'local_labels',json(COALESCE(json_extract(body,'$.local_labels'),'[]')))
FROM controlplane_state;
ALTER TABLE controlplane_state RENAME TO retired_membership_state;
