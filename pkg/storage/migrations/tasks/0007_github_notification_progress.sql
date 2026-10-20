-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE plugin_background ADD COLUMN diagnostic TEXT NOT NULL DEFAULT '';
-- Cancel outstanding history imports without discarding accepted snapshots,
-- cursors, pending notifications or delivery receipts. Wake stuck imports now.
UPDATE plugin_background
SET next = CASE WHEN COALESCE(json_extract(config,'$.existing'),'') != ''
                     AND json_extract(config,'$.existing') != COALESCE(json_extract(state,'$.manual'),'') THEN 0 ELSE next END,
    status = CASE WHEN COALESCE(json_extract(config,'$.existing'),'') != ''
                       AND json_extract(config,'$.existing') != COALESCE(json_extract(state,'$.manual'),'') THEN 'pending' ELSE status END,
    state = CASE WHEN json_type(state) = 'object' THEN
      json_set(json_remove(state,'$.manual'), '$.scan', NULL,
        '$.nextPollAt', CASE WHEN COALESCE(json_extract(config,'$.existing'),'') != ''
                                  AND json_extract(config,'$.existing') != COALESCE(json_extract(state,'$.manual'),'')
                            THEN 0 ELSE COALESCE(json_extract(state,'$.nextPollAt'),0) END)
      ELSE state END,
    config = json_remove(config,'$.existing','$.existing_account'),
    revision = revision + 1
WHERE id = 'github';
-- +migrate StatementEnd
