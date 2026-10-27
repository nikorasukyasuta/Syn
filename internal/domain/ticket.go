package domain

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrTicketAlreadyOpen   = errors.New("you already have an open ticket of this type")
	ErrTicketNotFound      = errors.New("ticket record not found")
	ErrTicketAlreadyClosed = errors.New("ticket is already closed")
	ErrUnauthorizedToClose = errors.New("only the ticket creator or server staff can close this ticket")
	ErrInvalidTicketType   = errors.New("invalid ticket type specified")
)

// TicketType identifies the category and purpose of a ticket.
type TicketType string

const (
	TicketTypeVerification  TicketType = "verification"
	TicketTypeReport        TicketType = "report"
	TicketTypeBug           TicketType = "bug"
	TicketTypeAccountIssue  TicketType = "account_issue"
	TicketTypeSupport       TicketType = "support"
	TicketTypeFilteredWords TicketType = "filtered_words"
)

// AllTicketTypes returns all supported ticket types.
var AllTicketTypes = []TicketType{
	TicketTypeVerification,
	TicketTypeReport,
	TicketTypeBug,
	TicketTypeAccountIssue,
	TicketTypeSupport,
	TicketTypeFilteredWords,
}

// IsValid validates whether a ticket type is supported.
func (t TicketType) IsValid() bool {
	switch t {
	case TicketTypeVerification, TicketTypeReport, TicketTypeBug, TicketTypeAccountIssue, TicketTypeSupport, TicketTypeFilteredWords:
		return true
	default:
		return false
	}
}

// DisplayName returns a human-readable title for the ticket type.
func (t TicketType) DisplayName() string {
	switch t {
	case TicketTypeVerification:
		return "Verification Ticket"
	case TicketTypeReport:
		return "Report Ticket"
	case TicketTypeBug:
		return "Bug Ticket"
	case TicketTypeAccountIssue:
		return "Account Issue Ticket"
	case TicketTypeSupport:
		return "Support Ticket"
	case TicketTypeFilteredWords:
		return "Filtered Words Ticket"
	default:
		return string(t)
	}
}

// Emoji returns the representative emoji for the ticket type.
func (t TicketType) Emoji() string {
	switch t {
	case TicketTypeVerification:
		return "🛡️"
	case TicketTypeReport:
		return "🚨"
	case TicketTypeBug:
		return "🐛"
	case TicketTypeAccountIssue:
		return "👤"
	case TicketTypeSupport:
		return "💬"
	case TicketTypeFilteredWords:
		return "🚫"
	default:
		return "🎫"
	}
}

// ChannelPrefix returns the channel name prefix for this ticket type.
func (t TicketType) ChannelPrefix() string {
	switch t {
	case TicketTypeVerification:
		return "verify"
	case TicketTypeReport:
		return "report"
	case TicketTypeBug:
		return "bug"
	case TicketTypeAccountIssue:
		return "account"
	case TicketTypeSupport:
		return "support"
	case TicketTypeFilteredWords:
		return "banned-word"
	default:
		return "ticket"
	}
}

// ParseTicketType normalizes and parses a string into a valid TicketType.
func ParseTicketType(raw string) (TicketType, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "verification", "verify":
		return TicketTypeVerification, nil
	case "report":
		return TicketTypeReport, nil
	case "bug":
		return TicketTypeBug, nil
	case "account_issue", "account", "account-issue":
		return TicketTypeAccountIssue, nil
	case "support":
		return TicketTypeSupport, nil
	case "filtered_words", "filtered-words", "banned_words", "banned-words", "filter":
		return TicketTypeFilteredWords, nil
	default:
		return "", ErrInvalidTicketType
	}
}

// TicketStatus tracks the operational lifecycle of a ticket.
type TicketStatus string

const (
	TicketStatusOpen   TicketStatus = "open"
	TicketStatusClosed TicketStatus = "closed"
)

// Ticket represents a private member support channel and audit record.
type Ticket struct {
	ID         string
	GuildID    string
	ChannelID  string
	CreatorID  string
	Type       TicketType
	Status     TicketStatus
	ClosedByID string
	Resolution string
	CreatedAt  time.Time
	ClosedAt   *time.Time
}

// TicketRepository defines persistence operations for the ticket system.
type TicketRepository interface {
	CreateTicket(ctx context.Context, t *Ticket) error
	GetTicketByChannelID(ctx context.Context, guildID, channelID string) (*Ticket, error)
	GetActiveTicket(ctx context.Context, guildID, creatorID string, ticketType TicketType) (*Ticket, error)
	CloseTicket(ctx context.Context, guildID, channelID, closedByID, resolution string, closedAt time.Time) error
	ListOpenTicketsByCreator(ctx context.Context, guildID, creatorID string) ([]*Ticket, error)
}
