-- 000005_word_filters_warnings.up.sql: Word filter violations and user warnings schema

-- 1. Create user_warnings table
CREATE TABLE IF NOT EXISTS user_warnings (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    guild_id        VARCHAR(32) NOT NULL,
    user_id         VARCHAR(32) NOT NULL,
    admin_id        VARCHAR(32) NOT NULL,
    message_content TEXT NOT NULL,
    rule_matched    VARCHAR(255),
    channel_id      VARCHAR(32),
    ticket_id       VARCHAR(64),
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_warnings_user ON user_warnings (guild_id, user_id);
CREATE INDEX IF NOT EXISTS idx_user_warnings_admin ON user_warnings (guild_id, admin_id);
CREATE INDEX IF NOT EXISTS idx_user_warnings_created_at ON user_warnings (created_at DESC);

-- 2. Create filter_violations table to track pending violations per ticket
CREATE TABLE IF NOT EXISTS filter_violations (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    guild_id        VARCHAR(32) NOT NULL,
    channel_id      VARCHAR(32) NOT NULL,
    ticket_id       VARCHAR(64),
    user_id         VARCHAR(32) NOT NULL,
    message_content TEXT NOT NULL,
    matched_word    VARCHAR(255) NOT NULL,
    origin_channel  VARCHAR(32) NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_filter_violations_channel ON filter_violations (channel_id);

-- 3. Update unique index on tickets to allow multiple filter violation tickets per user
DROP INDEX IF EXISTS idx_unique_open_ticket_per_type;
CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_open_ticket_per_type 
    ON tickets (guild_id, creator_id, ticket_type) 
    WHERE status = 'open' AND ticket_type NOT IN ('filtered_words', 'banned_words');

