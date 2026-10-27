package domain

import (
	"context"
	"time"
)

// FlashChannelConfig stores configuration and auto-delete TTL for a Flash and Dash channel.
type FlashChannelConfig struct {
	GuildID     string    `json:"guild_id"`
	ChannelID   string    `json:"channel_id"`
	DurationSec int       `json:"duration_sec"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Duration returns the configured time.Duration for the channel TTL (default 5m).
func (c *FlashChannelConfig) Duration() time.Duration {
	if c.DurationSec <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(c.DurationSec) * time.Second
}

// FlashMessage tracks an active message scheduled for automatic deletion.
type FlashMessage struct {
	ID        string    `json:"id"`
	GuildID   string    `json:"guild_id"`
	ChannelID string    `json:"channel_id"`
	MessageID string    `json:"message_id"`
	DeleteAt  time.Time `json:"delete_at"`
	CreatedAt time.Time `json:"created_at"`
}

// FlashRepository defines persistence operations for Flash & Dash channels and scheduled messages.
type FlashRepository interface {
	SaveChannelConfig(ctx context.Context, cfg *FlashChannelConfig) error
	GetChannelConfig(ctx context.Context, guildID, channelID string) (*FlashChannelConfig, error)
	ListChannelConfigs(ctx context.Context, guildID string) ([]*FlashChannelConfig, error)
	DeleteChannelConfig(ctx context.Context, guildID, channelID string) error

	SavePendingMessage(ctx context.Context, msg *FlashMessage) error
	DeletePendingMessage(ctx context.Context, channelID, messageID string) error
	ListExpiredMessages(ctx context.Context, before time.Time) ([]*FlashMessage, error)
	ListPendingMessages(ctx context.Context) ([]*FlashMessage, error)
}

