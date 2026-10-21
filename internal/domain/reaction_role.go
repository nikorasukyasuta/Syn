package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrProtectedRole     = errors.New("security violation: cannot bind a protected Staff, Helper, or 18+ Verified role to a reaction")
	ErrUnverifiedForRole = errors.New("member must be 18+ verified to claim this reaction role")
	ErrReactionNotFound  = errors.New("no reaction role configured for this message and emoji")
	ErrPanelNotFound     = errors.New("no reaction panel configured for this message")
)

// ReactionRole maps a specific message reaction to a Discord Role ID and Embed line item.
type ReactionRole struct {
	ID               string
	GuildID          string
	ChannelID        string
	MessageID        string
	EmojiAPIName     string // Used for Discord API reaction calls ("rope:123456")
	EmojiDisplay     string // Used inside the Discord Embed body ("<:rope:123456>")
	RoleID           string
	Description      string // E.g., "Interested in Rope Bondage"
	RequiresVerified bool
	CreatedByID      string
	CreatedAt        time.Time
}

// ReactionRoleRepository defines persistence operations for reaction role mappings.
type ReactionRoleRepository interface {
	UpsertMapping(ctx context.Context, rr *ReactionRole) error
	GetMapping(ctx context.Context, guildID, messageID, emojiAPIName string) (*ReactionRole, error)
	ListByMessage(ctx context.Context, guildID, messageID string) ([]*ReactionRole, error)
	DeleteMapping(ctx context.Context, guildID, messageID, emojiAPIName string) error
	SavePanel(ctx context.Context, panel *ReactionPanel) error
	GetPanel(ctx context.Context, guildID, messageID string) (*ReactionPanel, error)
	DeletePanel(ctx context.Context, guildID, messageID string) error
}

// ReactionPanel stores the visual design and local EC2 image filenames for a reaction role message.
type ReactionPanel struct {
	MessageID     string
	GuildID       string
	ChannelID     string
	Title         string
	Intro         string
	ColorHex      int    // Decimal representation of hex color (e.g., 0x9B59B6)
	BannerFile    string // Local filename in /app/assets/panels (e.g., "roles_banner.png")
	ThumbnailFile string // Local filename in /app/assets/panels (e.g., "crest.png")
	CreatedByID   string
	UpdatedAt     time.Time
}
