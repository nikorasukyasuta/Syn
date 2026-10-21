-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Store verification state without storing PII or raw ID images
CREATE TABLE IF NOT EXISTS member_verifications (
    discord_user_id VARCHAR(32) PRIMARY KEY,
    guild_id        VARCHAR(32) NOT NULL,
    is_verified     BOOLEAN NOT NULL DEFAULT FALSE,
    verified_by_id  VARCHAR(32) NOT NULL, -- Moderator Discord ID
    notes_encrypted BYTEA,                -- AES-256-GCM encrypted mod notes (optional)
    verified_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Immutable Audit Log for moderator actions (Enterprise compliance & accountability)
CREATE TABLE IF NOT EXISTS audit_logs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    actor_id        VARCHAR(32) NOT NULL,
    target_id       VARCHAR(32) NOT NULL,
    action          VARCHAR(64) NOT NULL,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_verifications_guild ON member_verifications(guild_id);
CREATE INDEX idx_audit_logs_target ON audit_logs(target_id);