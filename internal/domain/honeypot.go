package domain

import (
	"context"
	"errors"
	"time"
)

const (
	// DefaultQuarantineKickDelay is how long a quarantined spammer has to be released by staff
	// before they are automatically kicked from the server (3 days).
	DefaultQuarantineKickDelay = 72 * time.Hour

	// DefaultSpammerRoleName is the role name created/discovered by /honeypot setup.
	DefaultSpammerRoleName = "Spammer"

	// DefaultHoneypotChannelName is the channel name created by /honeypot setup.
	DefaultHoneypotChannelName = "⛔-do-not-type-here"
)

var (
	// ErrNotQuarantined is returned when releasing a user who has no active quarantine.
	ErrNotQuarantined = errors.New("user is not currently quarantined")
	// ErrHoneypotNotConfigured is returned when the honeypot channel or Spammer role is not set up.
	ErrHoneypotNotConfigured = errors.New("honeypot channel and spammer role are not configured; run /honeypot setup")
)

// QuarantineStatus is the lifecycle state of a spam quarantine record.
type QuarantineStatus string

const (
	QuarantineActive   QuarantineStatus = "active"
	QuarantineReleased QuarantineStatus = "released"
	QuarantineKicked   QuarantineStatus = "kicked"
	QuarantineLeft     QuarantineStatus = "left"
)

// HoneypotConfig stores the configured honeypot channel and Spammer role for a guild.
type HoneypotConfig struct {
	GuildID          string    `json:"guild_id"`
	ChannelID        string    `json:"channel_id"`
	SpammerRoleID    string    `json:"spammer_role_id"`
	WarningMessageID string    `json:"warning_message_id"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SpamQuarantine records a member caught by the honeypot. Message content is intentionally
// NOT persisted (data minimization); only identifiers needed to restore roles and enforce the kick deadline.
type SpamQuarantine struct {
	ID               string           `json:"id"`
	GuildID          string           `json:"guild_id"`
	UserID           string           `json:"user_id"`
	Username         string           `json:"username"`
	ChannelID        string           `json:"channel_id"`
	TriggerMessageID string           `json:"trigger_message_id"`
	RemovedRoleIDs   []string         `json:"removed_role_ids"`
	Status           QuarantineStatus `json:"status"`
	QuarantinedAt    time.Time        `json:"quarantined_at"`
	KickAt           time.Time        `json:"kick_at"`
	ResolvedAt       *time.Time       `json:"resolved_at,omitempty"`
	ResolvedBy       string           `json:"resolved_by"`
}

// HoneypotRepository defines persistence for honeypot configuration and spam quarantines.
type HoneypotRepository interface {
	SaveConfig(ctx context.Context, cfg *HoneypotConfig) error
	GetConfig(ctx context.Context, guildID string) (*HoneypotConfig, error)

	CreateQuarantine(ctx context.Context, q *SpamQuarantine) error
	GetActiveQuarantine(ctx context.Context, guildID, userID string) (*SpamQuarantine, error)
	ListActiveQuarantines(ctx context.Context, guildID string) ([]*SpamQuarantine, error)
	ResolveQuarantine(ctx context.Context, guildID, userID string, status QuarantineStatus, resolvedBy string) error
}

