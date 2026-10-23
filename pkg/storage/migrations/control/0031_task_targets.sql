-- +migrate Up
-- +migrate StatementBegin
UPDATE agent_link_connections SET metadata=json_set(metadata,'$.directory',json(pot_target_directory(json_extract(metadata,'$.directory')))) WHERE json_type(metadata,'$.directory')='object';
UPDATE agent_link_received_tasks SET payload=pot_target_received(payload,(SELECT json_extract(metadata,'$.local.address.node_id') FROM agent_link_connections WHERE json_extract(metadata,'$.peer_host.node_id')=json_extract(payload,'$.from.node_id') LIMIT 1),assistant_id) WHERE json_valid(payload);
UPDATE retired_task_submissions SET body=pot_target_submission(body);
UPDATE retired_task_submissions SET payload=pot_target_create(payload,body) WHERE json_valid(payload);
-- +migrate StatementEnd
