-- +migrate Up
-- Control ownership, LaunchDigest and all run/command identities stay intact.
-- Opaque command inputs/replies and prepared resources remain original evidence.
UPDATE task_runs SET body=pot_node_json(body,'run');
