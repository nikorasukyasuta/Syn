package domain

import (
	"time"

	"github.com/bwmarrin/discordgo"
)

// ServerTemplate represents a portable snapshot of a Discord server's roles, categories,
// channels, and permission overwrites exported from the production server so that an
// identical server structure can be provisioned on a local development Discord server.
type ServerTemplate struct {
	Version                     string                 `json:"version"`
	ExportedAt                  time.Time              `json:"exported_at"`
	SourceGuildID               string                 `json:"source_guild_id"`
	SourceGuildName             string                 `json:"source_guild_name"`
	VerificationLevel           int                    `json:"verification_level"`
	ExplicitContentFilter       int                    `json:"explicit_content_filter"`
	DefaultMessageNotifications int                    `json:"default_message_notifications"`
	Roles                       []RoleTemplate         `json:"roles"`
	Categories                  []CategoryTemplate     `json:"categories"`
	Channels                    []ChannelTemplate      `json:"channels"`
	BotMapping                  BotRoleTemplateMapping `json:"bot_mapping"`
}

// RoleTemplate captures a Discord role's visual, hierarchy, and permission bitmask properties.
type RoleTemplate struct {
	OriginalID  string `json:"original_id"`
	Name        string `json:"name"`
	Color       int    `json:"color"`
	Hoist       bool   `json:"hoist"`
	Permissions int64  `json:"permissions"`
	Mentionable bool   `json:"mentionable"`
	Position    int    `json:"position"`
	IsEveryone  bool   `json:"is_everyone"`
}

// PermissionOverwriteTemplate captures a channel or category permission overwrite in a
// server-agnostic format so role IDs can be mapped to newly created roles on the target server.
type PermissionOverwriteTemplate struct {
	TargetType       discordgo.PermissionOverwriteType `json:"target_type"`
	TargetOriginalID string                            `json:"target_original_id"`
	TargetRoleName   string                            `json:"target_role_name,omitempty"`
	TargetRoleColor  int                               `json:"target_role_color,omitempty"`
	IsEveryone       bool                              `json:"is_everyone,omitempty"`
	Allow            int64                             `json:"allow"`
	Deny             int64                             `json:"deny"`
}

// CategoryTemplate captures a Discord category channel and its permission overwrites.
type CategoryTemplate struct {
	OriginalID string                        `json:"original_id"`
	Name       string                        `json:"name"`
	Position   int                           `json:"position"`
	NSFW       bool                          `json:"nsfw"`
	Overwrites []PermissionOverwriteTemplate `json:"overwrites"`
}

// ChannelTemplate captures a Discord text, voice, announcement, stage, or forum channel.
type ChannelTemplate struct {
	OriginalID         string                        `json:"original_id"`
	Name               string                        `json:"name"`
	Type               discordgo.ChannelType         `json:"type"`
	Topic              string                        `json:"topic,omitempty"`
	Position           int                           `json:"position"`
	NSFW               bool                          `json:"nsfw"`
	RateLimitPerUser   int                           `json:"rate_limit_per_user,omitempty"`
	Bitrate            int                           `json:"bitrate,omitempty"`
	UserLimit          int                           `json:"user_limit,omitempty"`
	ParentOriginalID   string                        `json:"parent_original_id,omitempty"`
	ParentCategoryName string                        `json:"parent_category_name,omitempty"`
	Overwrites         []PermissionOverwriteTemplate `json:"overwrites"`
}

// BotRoleTemplateMapping tracks which original production role and channel IDs correspond
// to Syn's configured roles and channels, enabling automatic generation of config/discord_ids.dev.json.
type BotRoleTemplateMapping struct {
	VerifiedRoleOriginalID      string   `json:"verified_role_original_id"`
	IDVerifiedRoleOriginalID    string   `json:"id_verified_role_original_id"`
	UnverifiedRoleOriginalID    string   `json:"unverified_role_original_id"`
	StaffRoleOriginalIDs        []string `json:"staff_role_original_ids"`
	TicketCategoryOriginalID    string   `json:"ticket_category_original_id,omitempty"`
	TicketLogChannelOriginalID  string   `json:"ticket_log_channel_original_id,omitempty"`
	StaffAlertChannelOriginalID string   `json:"staff_alert_channel_original_id,omitempty"`
}

