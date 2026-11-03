-- 000009_flash_30m_and_spoiler_ttl.up.sql:
-- 1. Update Flash and Dash default channel timer to 30 minutes (1800 seconds)
-- 2. Create tables for user-defined Spoilered Photo TTL settings (30m to 14d) and scheduled deletions

ALTER TABLE flash_dash_channels
    ALTER COLUMN duration_sec SET DEFAULT 1800;

UPDATE flash_dash_channels
SET duration_sec = 1800,
    updated_at   = NOW()
WHERE duration_sec = 300;

CREATE TABLE IF NOT EXISTS user_spoiler_ttl_settings (
    guild_id     VARCHAR(32) NOT NULL,
    user_id      VARCHAR(32) NOT NULL,
    duration_sec INT NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (guild_id, user_id)
);

CREATE TABLE IF NOT EXISTS user_spoiler_messages (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    guild_id   VARCHAR(32) NOT NULL,
    channel_id VARCHAR(32) NOT NULL,
    message_id VARCHAR(32) NOT NULL UNIQUE,
    user_id    VARCHAR(32) NOT NULL,
    delete_at  TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_spoiler_messages_delete_at ON user_spoiler_messages (delete_at);
CREATE INDEX IF NOT EXISTS idx_user_spoiler_messages_user ON user_spoiler_messages (guild_id, user_id);
