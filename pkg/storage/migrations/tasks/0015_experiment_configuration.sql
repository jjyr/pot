-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE experiment_requests (
 assistant_id TEXT NOT NULL,
 request_id TEXT NOT NULL,
 input_hash TEXT NOT NULL,
 main_spec TEXT NOT NULL CHECK(json_valid(main_spec)),
 task_spec TEXT CHECK(task_spec IS NULL OR json_valid(task_spec)),
 PRIMARY KEY(assistant_id,request_id)
);
CREATE TABLE experiment_task_requests (
 assistant_id TEXT NOT NULL,
 request_id TEXT NOT NULL,
 experiment_id TEXT NOT NULL,
 PRIMARY KEY(assistant_id,request_id),
 FOREIGN KEY(assistant_id,experiment_id) REFERENCES experiment_requests(assistant_id,request_id)
);
-- +migrate StatementEnd
