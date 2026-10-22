CREATE TABLE
    IF NOT EXISTS reaction_panels (
        message_id VARCHAR(32) PRIMARY KEY,
        guild_id VARCHAR(32) NOT NULL,
        channel_id VARCHAR(32) NOT NULL,
        title VARCHAR(256) NOT NULL,
        intro TEXT NOT NULL,
        color_hex INTEGER NOT NULL DEFAULT 10181046, -- Default: 0x9B59B6 (Deep Purple)
        banner_file VARCHAR(255) NOT NULL DEFAULT '', -- Filename in /app/assets/panels (e.g., "kink_banner.png")
        thumbnail_file VARCHAR(255) NOT NULL DEFAULT '', -- Filename in /app/assets/panels (e.g., "server_crest.webp")
        created_by_id VARCHAR(32) NOT NULL,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

CREATE INDEX IF NOT EXISTS idx_reaction_panels_guild ON reaction_panels (guild_id, channel_id);