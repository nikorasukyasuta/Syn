package domain

import (
	"context"
	"strings"
	"time"
)

// TicketPanelType identifies an individual post in the read-only Ticket Creation Hub channel.
type TicketPanelType string

const (
	PanelTypeIDVerification TicketPanelType = "id_verification"
	PanelTypeSupport        TicketPanelType = "support"
	PanelTypeAccountIssue   TicketPanelType = "account_issue"
	PanelTypeReport         TicketPanelType = "report"
	PanelTypeBug            TicketPanelType = "bug"
)

// OrderedTicketPanelTypes defines the canonical top-to-bottom order of posts in the Ticket Hub channel.
var OrderedTicketPanelTypes = []TicketPanelType{
	PanelTypeIDVerification,
	PanelTypeSupport,
	PanelTypeAccountIssue,
	PanelTypeReport,
	PanelTypeBug,
}

// ParseTicketPanelType validates and normalizes a string into a TicketPanelType.
func ParseTicketPanelType(raw string) (TicketPanelType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "id_verification", "id-verification", "verification", "verify":
		return PanelTypeIDVerification, nil
	case "support":
		return PanelTypeSupport, nil
	case "account_issue", "account-issue", "account":
		return PanelTypeAccountIssue, nil
	case "report":
		return PanelTypeReport, nil
	case "bug":
		return PanelTypeBug, nil
	default:
		return "", ErrInvalidTicketType
	}
}

// IsValid returns true if the panel type is one of the 5 supported Ticket Hub flow types.
func (p TicketPanelType) IsValid() bool {
	switch p {
	case PanelTypeIDVerification, PanelTypeSupport, PanelTypeAccountIssue, PanelTypeReport, PanelTypeBug:
		return true
	default:
		return false
	}
}

// CustomID returns the Discord button custom_id triggered by this panel post.
func (p TicketPanelType) CustomID() string {
	if p == PanelTypeIDVerification {
		return "btn_id_verify_start"
	}
	return "btn_ticket_create:" + string(p)
}

// TicketPanelPost represents an individual ticket flow embed and button post in the Ticket Hub channel.
type TicketPanelPost struct {
	GuildID      string
	PanelType    TicketPanelType
	ChannelID    string
	MessageID    string
	Title        string
	Description  string
	Color        int
	ButtonLabel  string
	ButtonEmoji  string
	ButtonStyle  int
	CustomID     string
	DisplayOrder int
	UpdatedAt    time.Time
}

// DefaultTicketPanelPost returns the default embed configuration and button metadata for a given panel type.
func DefaultTicketPanelPost(guildID string, panelType TicketPanelType) *TicketPanelPost {
	now := time.Now().UTC()
	switch panelType {
	case PanelTypeIDVerification:
		return &TicketPanelPost{
			GuildID:   guildID,
			PanelType: PanelTypeIDVerification,
			Title:     "🪪 18+ Age & ID Verification",
			Description: "To access 18+ verified and mature community channels, complete our private age verification flow:\n\n" +
				"**How It Works:**\n" +
				"1. Click **Start 18+ ID Verification** below to open a private channel visible only to you and Server Staff.\n" +
				"2. Upload **one clear photo** showing your government-issued ID (**DOB visible**, all other personal details covered/blurred) next to a **handwritten physical note** with your Discord username and today's date.\n" +
				"3. Once approved by Staff, you receive the **18+ Verified** role and the verification channel is **permanently deleted**.",
			Color:        0x5865F2, // Blurple
			ButtonLabel:  "Start 18+ ID Verification",
			ButtonEmoji:  "🪪",
			ButtonStyle:  1, // PrimaryButton
			CustomID:     PanelTypeIDVerification.CustomID(),
			DisplayOrder: 1,
			UpdatedAt:    now,
		}
	case PanelTypeSupport:
		return &TicketPanelPost{
			GuildID:   guildID,
			PanelType: PanelTypeSupport,
			Title:     "💬 General Support Ticket",
			Description: "Have a general question about the server, community events, or need guidance from our team?\n\n" +
				"**How It Works:**\n" +
				"1. Click **Open Support Ticket** below to create a private support channel.\n" +
				"2. Describe your question or inquiry so a Staff member or Server Helper can assist you.\n" +
				"3. Click **Close Ticket** inside the channel once your question has been answered.",
			Color:        0x2ECC71, // Green
			ButtonLabel:  "Open Support Ticket",
			ButtonEmoji:  "💬",
			ButtonStyle:  3, // SuccessButton
			CustomID:     PanelTypeSupport.CustomID(),
			DisplayOrder: 2,
			UpdatedAt:    now,
		}
	case PanelTypeAccountIssue:
		return &TicketPanelPost{
			GuildID:   guildID,
			PanelType: PanelTypeAccountIssue,
			Title:     "👤 Account & Role Issue Ticket",
			Description: "Experiencing issues with your server roles, channel permissions, or transferring verification from a previous Discord account?\n\n" +
				"**How It Works:**\n" +
				"1. Click **Open Account Issue Ticket** below to open a private session with Staff.\n" +
				"2. Explain the missing roles, permission issue, or account change.\n" +
				"3. Staff will review your account records and update your roles accordingly.",
			Color:        0x3498DB, // Blue
			ButtonLabel:  "Open Account Issue Ticket",
			ButtonEmoji:  "👤",
			ButtonStyle:  2, // SecondaryButton
			CustomID:     PanelTypeAccountIssue.CustomID(),
			DisplayOrder: 3,
			UpdatedAt:    now,
		}
	case PanelTypeReport:
		return &TicketPanelPost{
			GuildID:   guildID,
			PanelType: PanelTypeReport,
			Title:     "🚨 Member & Rule Violation Report",
			Description: "Need to confidentially report harassment, boundary/safeword violations, or a breach of community rules?\n\n" +
				"**How It Works:**\n" +
				"1. Click **Open Report Ticket** below to open a strictly confidential channel with Server Staff.\n" +
				"2. Provide the member's username/ID, relevant context, message links, or screenshots.\n" +
				"3. Staff will investigate promptly and take appropriate moderation action.",
			Color:        0xE74C3C, // Red
			ButtonLabel:  "Open Report Ticket",
			ButtonEmoji:  "🚨",
			ButtonStyle:  4, // DangerButton
			CustomID:     PanelTypeReport.CustomID(),
			DisplayOrder: 4,
			UpdatedAt:    now,
		}
	case PanelTypeBug:
		return &TicketPanelPost{
			GuildID:   guildID,
			PanelType: PanelTypeBug,
			Title:     "🐛 Bot & Server Bug Report",
			Description: "Found a technical glitch with the Syn bot, broken reaction roles, or a channel configuration bug?\n\n" +
				"**How It Works:**\n" +
				"1. Click **Open Bug Report** below to open a technical report channel.\n" +
				"2. Share what command or button you used, what happened, and any error messages displayed.\n" +
				"3. Our administrators will diagnose and resolve the issue.",
			Color:        0xF59E0B, // Amber
			ButtonLabel:  "Open Bug Report",
			ButtonEmoji:  "🐛",
			ButtonStyle:  2, // SecondaryButton
			CustomID:     PanelTypeBug.CustomID(),
			DisplayOrder: 5,
			UpdatedAt:    now,
		}
	default:
		return nil
	}
}

// TicketPanelRepository defines persistence operations for the dynamic Ticket Creation Hub posts.
type TicketPanelRepository interface {
	GetPanelPost(ctx context.Context, guildID string, panelType TicketPanelType) (*TicketPanelPost, error)
	ListPanelPosts(ctx context.Context, guildID string) ([]*TicketPanelPost, error)
	SavePanelPost(ctx context.Context, post *TicketPanelPost) error
}

