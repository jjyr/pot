-- +migrate Up
-- +migrate StatementBegin
INSERT INTO assistant_profiles(assistant_id,avatar)
SELECT id,pot_default_avatar(request_id) FROM assistant_definitions
WHERE true
ON CONFLICT(assistant_id) DO UPDATE SET avatar=excluded.avatar
WHERE assistant_profiles.avatar='';

UPDATE assistant_profile_requests
SET response=json_set(response,'$.avatar',pot_default_avatar(json_extract(response,'$.request_id')))
WHERE COALESCE(json_extract(response,'$.avatar'),'')='';
-- +migrate StatementEnd
