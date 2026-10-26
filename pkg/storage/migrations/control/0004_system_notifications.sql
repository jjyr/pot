-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE notification_secrets (id TEXT PRIMARY KEY, value BLOB NOT NULL);
CREATE TABLE notification_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 body TEXT NOT NULL, created_ms INTEGER NOT NULL
);
CREATE TABLE notification_devices (
 id TEXT PRIMARY KEY, binding TEXT NOT NULL, control_id TEXT NOT NULL, channel TEXT NOT NULL CHECK(channel IN ('local','push')),
 enabled INTEGER NOT NULL, permission TEXT NOT NULL, locale TEXT NOT NULL,
 expires_ms INTEGER NOT NULL, revision INTEGER NOT NULL DEFAULT 1, after_sequence INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE notification_push_subscriptions (
 device TEXT PRIMARY KEY REFERENCES notification_devices(id) ON DELETE CASCADE,
 body TEXT NOT NULL, fingerprint TEXT NOT NULL, invalidated INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX notification_event_retention ON notification_events(created_ms);
CREATE TABLE notification_views (
 device TEXT NOT NULL REFERENCES notification_devices(id) ON DELETE CASCADE,
 session TEXT NOT NULL, body TEXT NOT NULL, sequence INTEGER NOT NULL, expires_ms INTEGER NOT NULL,
 PRIMARY KEY(device,session)
);
CREATE TABLE notification_deliveries (
 event TEXT NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
 device TEXT NOT NULL REFERENCES notification_devices(id) ON DELETE CASCADE,
 channel TEXT NOT NULL CHECK(channel IN ('local','push')),
 revision INTEGER NOT NULL, state TEXT NOT NULL CHECK(state IN ('suppressed','queued','sending','accepted','failed','unknown','expired')),
 attempts INTEGER NOT NULL DEFAULT 0, next_ms INTEGER NOT NULL, updated_ms INTEGER NOT NULL,
 PRIMARY KEY(event,device)
);
CREATE INDEX notification_delivery_queue ON notification_deliveries(state,next_ms);
-- +migrate StatementEnd
