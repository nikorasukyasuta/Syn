package domain

import (
	"context"
	"time"
)

// FilterScope distinguishes between server-wide (global) and channel-specific word filters.
type FilterScope string

const (
	FilterScopeGlobal  FilterScope = "global"
	FilterScopeChannel FilterScope = "channel"
)

// FilterMatch describes a detected filter violation.
type FilterMatch struct {
	Word      string
	Scope     FilterScope
	ChannelID string
}

// UserWarning represents a formal moderation warning issued to a user for language/rule violations.
type UserWarning struct {
	ID             string    `json:"id"`
	GuildID        string    `json:"guild_id"`
	UserID         string    `json:"user_id"`
	AdminID        string    `json:"admin_id"`
	MessageContent string    `json:"message_content"`
	RuleMatched    string    `json:"rule_matched"`
	ChannelID      string    `json:"channel_id"`
	TicketID       string    `json:"ticket_id"`
	Reason         string    `json:"reason"`
	CreatedAt      time.Time `json:"created_at"`
}

// FilterViolation represents the context of a deleted violating message stored for staff ticket review.
type FilterViolation struct {
	ID             string    `json:"id"`
	GuildID        string    `json:"guild_id"`
	ChannelID      string    `json:"channel_id"` // Ticket channel ID
	TicketID       string    `json:"ticket_id"`
	UserID         string    `json:"user_id"`
	MessageContent string    `json:"message_content"`
	MatchedWord    string    `json:"matched_word"`
	OriginChannel  string    `json:"origin_channel"`
	CreatedAt      time.Time `json:"created_at"`
}

// WarningRepository defines persistence contracts for user moderation warnings and pending filter violations.
type WarningRepository interface {
	SaveWarning(ctx context.Context, w *UserWarning) error
	GetWarningsByUserID(ctx context.Context, guildID, userID string) ([]*UserWarning, error)
	GetWarningCountByUserID(ctx context.Context, guildID, userID string) (int, error)
	SaveViolation(ctx context.Context, v *FilterViolation) error
	GetViolationByChannelID(ctx context.Context, guildID, channelID string) (*FilterViolation, error)
}

