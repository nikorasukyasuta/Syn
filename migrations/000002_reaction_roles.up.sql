CREATE TABLE IF NOT EXISTS reaction_roles (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    guild_id          VARCHAR(32) NOT NULL,
    channel_id        VARCHAR(32) NOT NULL,
    message_id        VARCHAR(32) NOT NULL,
    emoji_api_name    VARCHAR(128) NOT NULL, -- API format ("❤️" or "rope:123456789")
    emoji_display     VARCHAR(128) NOT NULL, -- Renderable format ("❤️" or "<:rope:123456789>")
    role_id           VARCHAR(32) NOT NULL,
    description       VARCHAR(120) NOT NULL DEFAULT '', -- Optional label shown on the embed
    requires_verified BOOLEAN NOT NULL DEFAULT TRUE,
    created_by_id     VARCHAR(32) NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(message_id, emoji_api_name)
);

CREATE INDEX idx_reaction_roles_lookup ON reaction_roles(guild_id, message_id, emoji_api_name);
CREATE INDEX idx_reaction_roles_message ON reaction_roles(guild_id, message_id);