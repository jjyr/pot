-- +migrate Up
-- +migrate StatementBegin
ALTER TABLE assistant_profiles ADD COLUMN character TEXT NOT NULL DEFAULT '';

INSERT INTO assistant_profiles(assistant_id,avatar,character)
SELECT id,'',pot_avatar_character(created_name,request_id) FROM assistant_definitions WHERE true
ON CONFLICT(assistant_id) DO UPDATE SET character=excluded.character;

UPDATE assistant_profiles
SET avatar=pot_character_avatar(character,(SELECT request_id FROM assistant_definitions WHERE id=assistant_id))
WHERE avatar='' OR avatar=pot_default_avatar((SELECT request_id FROM assistant_definitions WHERE id=assistant_id));

UPDATE assistant_profile_requests
SET response=json_set(response,'$.avatar',pot_character_avatar(
 COALESCE((SELECT character FROM assistant_profiles WHERE assistant_id=json_extract(response,'$.id')),
 pot_avatar_character(json_extract(response,'$.name'),json_extract(response,'$.request_id'))),
 json_extract(response,'$.request_id')))
WHERE COALESCE(json_extract(response,'$.avatar'),'')=''
 OR json_extract(response,'$.avatar')=pot_default_avatar(json_extract(response,'$.request_id'));
-- +migrate StatementEnd
