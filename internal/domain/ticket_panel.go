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
			Title:     "🪪  18+ Age & ID Verification",
			Description: "Gain full access to verified adult discussion and mature community spaces.\n\n" +
				"**Verification Checklist:**\n" +
				"• **Government-Issued Photo ID** — showing your date of birth clearly *(all other personal details may be blurred or covered)*\n" +
				"• **Handwritten Verification Note** — a physical paper note next to your ID showing your Discord username & today's date\n" +
				"• **Selfie with ID** — a clear photo of you holding your physical ID next to your face\n\n" +
				"**Privacy Commitment:**\n" +
				"Your session takes place in an isolated, encrypted channel visible only to you and authorized reviewers. Once approved, **your images and channel are permanently deleted**.",
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
			Title:     "💬  General Support & Inquiries",
			Description: "Need guidance, assistance with server features, or have a question for our team?\n\n" +
				"**How It Works:**\n" +
				"• Click **Open Support Ticket** below to launch a private dialogue with Staff & Helpers.\n" +
				"• Share your question or request with as much detail as possible.\n" +
				"• A team member will assist you promptly. When finished, click **Close Ticket**.",
			Color:        0x2ECC71, // Emerald Green
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
			Title:     "👤  Account & Role Assistance",
			Description: "Having trouble with your server roles, channel access permissions, or account migration?\n\n" +
				"**How It Works:**\n" +
				"• Click **Open Account Ticket** below to open a private session with Server Staff.\n" +
				"• Describe the missing roles, permission anomaly, or account change.\n" +
				"• Staff will verify your account status and adjust your profile permissions.",
			Color:        0x3498DB, // Blue
			ButtonLabel:  "Open Account Ticket",
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
			Title:     "🚨  Confidential Safety & Incident Report",
			Description: "Report harassment, boundary violations, safeword infractions, or server rule breaches in strict confidence.\n\n" +
				"**How It Works:**\n" +
				"• Click **Open Confidential Report** below to reach Server Staff directly.\n" +
				"• Provide the username/ID, incident context, message links, and any relevant screenshots.\n" +
				"• Our administrative team handles all reports with strict privacy and care.",
			Color:        0xED4245, // Coral Red
			ButtonLabel:  "Open Confidential Report",
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
			Title:     "🐛  Technical Bug & Bot Diagnostics",
			Description: "Encountered a glitch with bot commands, reaction panels, or server integrations?\n\n" +
				"**How It Works:**\n" +
				"• Click **Report Technical Bug** below to open a technical support thread.\n" +
				"• Include the command/button triggered, what happened, and any error message displayed.\n" +
				"• Our developers and administrators will investigate and deploy a fix.",
			Color:        0xF59E0B, // Amber
			ButtonLabel:  "Report Technical Bug",
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
