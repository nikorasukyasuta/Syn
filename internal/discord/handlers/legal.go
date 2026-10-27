package handlers

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
)

// LegalHandler manages the /terms and /privacy slash commands, ephemerally presenting
// summaries and direct links to the hosted web portal.
type LegalHandler struct {
	cfg *config.Config
}

// NewLegalHandler constructs a new LegalHandler.
func NewLegalHandler(cfg *config.Config) *LegalHandler {
	return &LegalHandler{
		cfg: cfg,
	}
}

// TermsCommandDefinition defines the slash command schema for /terms.
func TermsCommandDefinition() *discordgo.ApplicationCommand {
	dmPermission := false
	return &discordgo.ApplicationCommand{
		Name:         "terms",
		Description:  "Display community Terms and Conditions and web link (Ephemeral)",
		DMPermission: &dmPermission,
	}
}

// PrivacyCommandDefinition defines the slash command schema for /privacy.
func PrivacyCommandDefinition() *discordgo.ApplicationCommand {
	dmPermission := false
	return &discordgo.ApplicationCommand{
		Name:         "privacy",
		Description:  "Display community Privacy Policy and web link (Ephemeral)",
		DMPermission: &dmPermission,
	}
}

// HandleTermsCommand ephemerally renders the Terms & Conditions summary and web link.
func (h *LegalHandler) HandleTermsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	baseURL := strings.TrimRight(h.cfg.WebBaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	termsURL := baseURL + "/terms"

	embed := &discordgo.MessageEmbed{
		Title: "📜 Terms & Conditions • Syn Discord Suite",
		Description: "By participating in this community and interacting with the Syn Bot, you agree to these Terms and Conditions.\n\n" +
			fmt.Sprintf("🌐 **Full Web Version:** [Read Online](%s)", termsURL),
		Color: 0x5865F2,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "🔞 1. Age Requirement & 18+ Verification",
				Value: "All members accessing verified or mature channels must be at least 18 years old. " +
					"Submitting fraudulent or altered ID credentials results in an immediate permanent ban.",
			},
			{
				Name: "🛑 2. Boundaries & Safeword (RED)",
				Value: "When a member or staff issues `/red` or reacts with 🛑, all participants must immediately halt the topic. " +
					"Failure to respect boundaries may result in automatic timeouts or moderation action.",
			},
			{
				Name: "📸 3. Flash & Dash Ephemeral Channels",
				Value: "All photos uploaded to designated Flash and Dash channels are automatically spoilered and permanently deleted " +
					"after the staff-configured expiration timer. Screenshotting or saving ephemeral photos without consent is strictly prohibited.",
			},
			{
				Name: "🛡️ 4. Automated Filtering & Moderation",
				Value: "Messages containing prohibited slurs or hate speech are deleted immediately and trigger a staff review ticket. " +
					"Staff reserve the right to issue formal warnings, timeouts, or bans for disruptions.",
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Discord Suite • Ephemeral • Only you can see this message",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	buttonRow := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label: "View Full Terms Online",
				Style: discordgo.LinkButton,
				URL:   termsURL,
				Emoji: &discordgo.ComponentEmoji{Name: "📜"},
			},
		},
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{buttonRow},
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

// HandlePrivacyCommand ephemerally renders the Privacy Policy summary and web link.
func (h *LegalHandler) HandlePrivacyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	baseURL := strings.TrimRight(h.cfg.WebBaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	privacyURL := baseURL + "/privacy"

	embed := &discordgo.MessageEmbed{
		Title: "🔒 Privacy Policy • Syn Discord Suite",
		Description: "We take your data privacy seriously and adhere strictly to data minimization and automated deletion standards.\n\n" +
			fmt.Sprintf("🌐 **Full Web Version:** [Read Online](%s)", privacyURL),
		Color: 0x10B981, // Emerald / Green
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "📋 1. Minimal Data Collection",
				Value: "We collect only essential Discord snowflake IDs (User ID, Channel ID, Message ID) necessary for role verification, " +
					"ticket operations, and audit logging. We do not sell or monetize personal data.",
			},
			{
				Name: "📸 2. No Image Retention on Bot Servers",
				Value: "Media shared in Flash and Dash channels is stored exclusively on Discord's CDN, never on our database or disks. " +
					"When auto-spoilering an image, the stream is processed transiently in-memory and immediately discarded.",
			},
			{
				Name: "⏱️ 3. Automated Deletion Safeguards",
				Value: "Photos and messages sent in Flash and Dash channels are automatically and irreversibly deleted from Discord " +
					"when their time-to-live (TTL) expiration timer elapses.",
			},
			{
				Name: "🔐 4. Encrypted & Isolated Storage",
				Value: "Persistent records (verification status, moderation warnings) are stored in an encrypted PostgreSQL database " +
					"on an isolated Docker network with no direct public internet exposure.",
			},
			{
				Name: "📬 5. Data Access & Deletion Requests",
				Value: "You may request an export or deletion of your moderation records by opening a ticket with `/ticket open type:support`.",
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Discord Suite • Ephemeral • Only you can see this message",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	buttonRow := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label: "View Full Privacy Policy Online",
				Style: discordgo.LinkButton,
				URL:   privacyURL,
				Emoji: &discordgo.ComponentEmoji{Name: "🔒"},
			},
		},
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{buttonRow},
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

