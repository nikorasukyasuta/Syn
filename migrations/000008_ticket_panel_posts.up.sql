-- Persistent Ticket Hub Posts for in-place dynamic embed updates without changing channel position
CREATE TABLE IF NOT EXISTS ticket_panel_posts (
    guild_id       VARCHAR(32) NOT NULL,
    panel_type     VARCHAR(32) NOT NULL, -- id_verification, support, account_issue, report, bug
    channel_id     VARCHAR(32) NOT NULL,
    message_id     VARCHAR(32) NOT NULL,
    title          TEXT NOT NULL,
    description    TEXT NOT NULL,
    color          INT NOT NULL DEFAULT 5793266,
    button_label   VARCHAR(80) NOT NULL,
    button_emoji   VARCHAR(32) NOT NULL,
    button_style   INT NOT NULL DEFAULT 1,
    display_order  INT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (guild_id, panel_type)
);

CREATE INDEX IF NOT EXISTS idx_ticket_panel_posts_guild_order ON ticket_panel_posts(guild_id, display_order);

