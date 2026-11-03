package domain

import (
	"context"
	"time"
)

const (
	// MinSpoilerPhotoTTL is the minimum 30-minute timer users can set for their spoilered photos.
	MinSpoilerPhotoTTL    = 30 * time.Minute
	MinSpoilerPhotoTTLSec = 1800

	// MaxSpoilerPhotoTTL is the maximum 14-day timer users can set for their spoilered photos.
	MaxSpoilerPhotoTTL    = 14 * 24 * time.Hour
	MaxSpoilerPhotoTTLSec = 14 * 24 * 3600
)

// UserSpoilerTTLSetting stores a user's personal auto-deletion TTL preference for spoilered photos.
type UserSpoilerTTLSetting struct {
	GuildID     string    `json:"guild_id"`
	UserID      string    `json:"user_id"`
	DurationSec int       `json:"duration_sec"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Duration returns the configured time.Duration for the user's spoilered photo TTL.
func (s *UserSpoilerTTLSetting) Duration() time.Duration {
	if s == nil || s.DurationSec <= 0 {
		return 0
	}
	return time.Duration(s.DurationSec) * time.Second
}

// UserSpoilerMessage tracks an active spoilered photo message scheduled for automatic deletion.
type UserSpoilerMessage struct {
	ID        string    `json:"id"`
	GuildID   string    `json:"guild_id"`
	ChannelID string    `json:"channel_id"`
	MessageID string    `json:"message_id"`
	UserID    string    `json:"user_id"`
	DeleteAt  time.Time `json:"delete_at"`
	CreatedAt time.Time `json:"created_at"`
}

// SpoilerTTLRepository defines persistence operations for user spoiler photo TTL settings and scheduled messages.
type SpoilerTTLRepository interface {
	SaveUserSetting(ctx context.Context, setting *UserSpoilerTTLSetting) error
	GetUserSetting(ctx context.Context, guildID, userID string) (*UserSpoilerTTLSetting, error)
	ListUserSettings(ctx context.Context, guildID string) ([]*UserSpoilerTTLSetting, error)
	DeleteUserSetting(ctx context.Context, guildID, userID string) error

	SavePendingMessage(ctx context.Context, msg *UserSpoilerMessage) error
	DeletePendingMessage(ctx context.Context, channelID, messageID string) error
	ListExpiredMessages(ctx context.Context, before time.Time) ([]*UserSpoilerMessage, error)
	ListPendingMessages(ctx context.Context) ([]*UserSpoilerMessage, error)
}
