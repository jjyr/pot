-- The sql-migrate receipt layout. Created in the same transaction as the entire
-- initial migration batch, including when the first attempt fails or crashes.
CREATE TABLE IF NOT EXISTS pot_migrations (
    id VARCHAR(255) NOT NULL PRIMARY KEY,
    applied_at DATETIME
);
