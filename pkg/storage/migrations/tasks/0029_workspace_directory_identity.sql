-- +migrate Up
-- +migrate StatementBegin
-- Device/inode alone can be reused after directory removal. Old receipts do not
-- contain original creation evidence; never synthesize it from the current path.
-- Keep every path, old identity and run receipt, but fence continuation/downloads.
UPDATE run_workspaces
 SET body=json_set(body,
   '$.status','identity_unverified',
   '$.error','Original workspace directory creation identity is unavailable after upgrade; files were preserved. An explicitly selected new Task may reuse the directory.')
 WHERE json_extract(body,'$.status')='ready'
   AND COALESCE(json_extract(body,'$.directory_id'),'') NOT LIKE 'directory-v2:%';
-- +migrate StatementEnd
