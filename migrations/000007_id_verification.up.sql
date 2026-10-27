-- Dedicated ID Verification Request Sessions
CREATE TABLE IF NOT EXISTS id_verification_requests (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    guild_id        VARCHAR(32) NOT NULL,
    channel_id      VARCHAR(32) NOT NULL UNIQUE,
    user_id         VARCHAR(32) NOT NULL,
    status          VARCHAR(32) NOT NULL DEFAULT 'pending', -- pending, approved, denied, canceled
    reviewed_by_id  VARCHAR(32),
    review_reason   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_id_verifications_guild_user_status ON id_verification_requests(guild_id, user_id, status);
CREATE INDEX IF NOT EXISTS idx_id_verifications_channel ON id_verification_requests(channel_id);

