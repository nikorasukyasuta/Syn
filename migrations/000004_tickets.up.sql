-- 000004_tickets.up.sql: Enterprise Ticket System table and constraints

CREATE TABLE IF NOT EXISTS tickets (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    guild_id         VARCHAR(32) NOT NULL,
    channel_id       VARCHAR(32) NOT NULL UNIQUE,
    creator_id       VARCHAR(32) NOT NULL,
    ticket_type      VARCHAR(32) NOT NULL, -- verification, report, bug, account_issue, support
    status           VARCHAR(32) NOT NULL DEFAULT 'open', -- open, closed
    closed_by_id     VARCHAR(32),
    resolution       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at        TIMESTAMPTZ
);

-- Enforce maximum 1 active/open ticket of each type per user in a guild
CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_open_ticket_per_type 
    ON tickets (guild_id, creator_id, ticket_type) 
    WHERE status = 'open';

CREATE INDEX IF NOT EXISTS idx_tickets_creator ON tickets (guild_id, creator_id);
CREATE INDEX IF NOT EXISTS idx_tickets_channel ON tickets (channel_id);
CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets (guild_id, status);

