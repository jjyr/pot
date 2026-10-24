-- +migrate Up
CREATE TABLE message_files (
 owner TEXT NOT NULL,
 conversation TEXT NOT NULL,
 message TEXT NOT NULL,
 revision TEXT NOT NULL,
 ref TEXT NOT NULL PRIMARY KEY,
 start INTEGER NOT NULL,
 end INTEGER NOT NULL,
 href TEXT NOT NULL,
 label TEXT NOT NULL,
 block INTEGER NOT NULL,
 source TEXT NOT NULL,
 state TEXT NOT NULL,
 resource TEXT NOT NULL DEFAULT '',
 UNIQUE(owner, conversation, message, revision, start, end)
);
CREATE INDEX message_files_lookup ON message_files(owner,conversation,message);
