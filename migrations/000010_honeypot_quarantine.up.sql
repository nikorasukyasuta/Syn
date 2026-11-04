-- 000010_honeypot_quarantine.up.sql:
-- 1. Persist the honeypot (spam trap) channel and Spammer role configured via /honeypot setup
-- 2. Track quarantined spammers, the roles stripped from them, and their automatic kick deadline

CREATE TABLE IF NOT EXISTS honeypot_config (
    guild_id           VARCHAR(32) PRIMARY KEY,
    channel_id         VARCHAR(32) NOT NULL DEFAULT '',
    spammer_role_id    VARCHAR(32) NOT NULL DEFAULT '',
    warning_message_id VARCHAR(32) NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS spam_quarantines (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    guild_id           VARCHAR(32) NOT NULL,
    user_id            VARCHAR(32) NOT NULL,
    username           VARCHAR(100) NOT NULL DEFAULT '',
    channel_id         VARCHAR(32) NOT NULL DEFAULT '',
    trigger_message_id VARCHAR(32) NOT NULL DEFAULT '',
    removed_role_ids   TEXT[] NOT NULL DEFAULT '{}',
    status             VARCHAR(16) NOT NULL DEFAULT 'active', -- active | released | kicked | left
    quarantined_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    kick_at            TIMESTAMPTZ NOT NULL,
    resolved_at        TIMESTAMPTZ,
    resolved_by        VARCHAR(64) NOT NULL DEFAULT ''
);

-- A user can only have one active quarantine per guild at a time
CREATE UNIQUE INDEX IF NOT EXISTS idx_spam_quarantines_active_user
    ON spam_quarantines (guild_id, user_id) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_spam_quarantines_kick_at
    ON spam_quarantines (kick_at) WHERE status = 'active';

