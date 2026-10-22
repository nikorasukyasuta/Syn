-- 000006_flash_dash.up.sql: Flash and Dash photo channels and auto-deletion tracking

CREATE TABLE IF NOT EXISTS flash_dash_channels (
    guild_id     VARCHAR(32) NOT NULL,
    channel_id   VARCHAR(32) NOT NULL PRIMARY KEY,
    duration_sec INT NOT NULL DEFAULT 300, -- default 5 minutes
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS flash_dash_messages (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    guild_id   VARCHAR(32) NOT NULL,
    channel_id VARCHAR(32) NOT NULL,
    message_id VARCHAR(32) NOT NULL UNIQUE,
    delete_at  TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_flash_dash_messages_delete_at ON flash_dash_messages (delete_at);
CREATE INDEX IF NOT EXISTS idx_flash_dash_messages_channel ON flash_dash_messages (channel_id);

