package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/service"
)

// HTTPClient defines the interface for downloading attachments.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// FlashHandler coordinates Discord events for Flash and Dash ephemeral photo channels,
// enforcing auto-spoilering and TTL-based auto-deletion.
type FlashHandler struct {
	svc        *service.FlashDashService
	cfg        *config.Config
	logger     *slog.Logger
	httpClient HTTPClient
}

// NewFlashHandler constructs a new FlashHandler.
func NewFlashHandler(
	svc *service.FlashDashService,
	cfg *config.Config,
	logger *slog.Logger,
) *FlashHandler {
	return &FlashHandler{
		svc:    svc,
		cfg:    cfg,
		logger: logger,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// SetHTTPClient overrides the HTTP client for testing.
func (h *FlashHandler) SetHTTPClient(client HTTPClient) {
	h.httpClient = client
}

// FlashCommandDefinition returns the Discord slash command schema for /flash.
func FlashCommandDefinition() *discordgo.ApplicationCommand {
	var defaultPerms int64 = 0
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:                     "flash",
		Description:              "Configure Flash & Dash photo channels with auto-spoilering and auto-deletion (Staff Only)",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "set-timer",
				Description: "Set auto-deletion timer and enable Flash & Dash for a channel",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "duration",
						Description: "Time before photos/messages are deleted (e.g. 30s, 5m, 1h, 24h)",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
					},
					{
						Name:        "channel",
						Description: "Target channel (defaults to current channel)",
						Type:        discordgo.ApplicationCommandOptionChannel,
						Required:    false,
					},
				},
			},
			{
				Name:        "disable",
				Description: "Disable Flash & Dash auto-deletion for a channel",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "channel",
						Description: "Target channel (defaults to current channel)",
						Type:        discordgo.ApplicationCommandOptionChannel,
						Required:    false,
					},
				},
			},
			{
				Name:        "status",
				Description: "View Flash & Dash channels and active deletion timers",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "channel",
						Description: "Inspect specific channel (optional)",
						Type:        discordgo.ApplicationCommandOptionChannel,
						Required:    false,
					},
				},
			},
		},
	}
}

// HandleFlashCommand routes subcommands for /flash.
func (h *FlashHandler) HandleFlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		h.respondError(s, i, "Invalid command usage: subcommand required.")
		return
	}

	subcommand := options[0].Name
	switch subcommand {
	case "set-timer":
		h.handleSetTimer(s, i, options[0].Options)
	case "disable":
		h.handleDisable(s, i, options[0].Options)
	case "status":
		h.handleStatus(s, i, options[0].Options)
	default:
		h.respondError(s, i, fmt.Sprintf("Unknown subcommand: %s", subcommand))
	}
}

func (h *FlashHandler) handleSetTimer(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	var rawDuration string
	targetChannelID := i.ChannelID

	for _, opt := range opts {
		switch opt.Name {
		case "duration":
			rawDuration = opt.StringValue()
		case "channel":
			if ch := opt.ChannelValue(s); ch != nil {
				targetChannelID = ch.ID
			}
		}
	}

	d, err := ParseFlashDuration(rawDuration)
	if err != nil {
		h.respondError(s, i, fmt.Sprintf("Invalid duration: %s. Examples: `30s`, `5m`, `1h`, `24h`.", err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	if err := h.svc.ConfigureChannel(ctx, guildID, targetChannelID, d); err != nil {
		h.logger.Error("failed to configure flash channel", "channel_id", targetChannelID, "error", err)
		h.respondError(s, i, "Failed to save Flash & Dash configuration to database.")
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       "⚡ Flash & Dash Channel Configured",
		Description: fmt.Sprintf("Channel <#%s> is now a designated **Flash and Dash** channel.", targetChannelID),
		Color:       0x5865F2,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Auto-Deletion Timer",
				Value:  fmt.Sprintf("`%s` (%d seconds)", d.String(), int(d.Seconds())),
				Inline: true,
			},
			{
				Name:   "Auto-Spoilering",
				Value:  "✅ Enabled (All unspoilered photos will be re-posted as spoilers)",
				Inline: true,
			},
			{
				Name:   "Content Policy",
				Value:  "📸 **Photos Only** (Text-only comments & non-photo files are deleted immediately)",
				Inline: false,
			},
			{
				Name:  "Channel Guidelines",
				Value: "All photos uploaded to this channel (along with their captions) will automatically expire and be deleted from Discord after the configured timer.",
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Discord Suite • Flash & Dash Safe Storage",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

func (h *FlashHandler) handleDisable(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	targetChannelID := i.ChannelID
	for _, opt := range opts {
		if opt.Name == "channel" {
			if ch := opt.ChannelValue(s); ch != nil {
				targetChannelID = ch.ID
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	if err := h.svc.RemoveChannel(ctx, guildID, targetChannelID); err != nil {
		h.logger.Error("failed to disable flash channel", "channel_id", targetChannelID, "error", err)
		h.respondError(s, i, "Failed to remove Flash & Dash channel configuration.")
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       "⚡ Flash & Dash Channel Disabled",
		Description: fmt.Sprintf("Flash & Dash auto-spoilering and auto-deletion have been disabled for <#%s>.", targetChannelID),
		Color:       0xF59E0B, // Amber/Warning color
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

func (h *FlashHandler) handleStatus(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	var specificChannelID string
	for _, opt := range opts {
		if opt.Name == "channel" {
			if ch := opt.ChannelValue(s); ch != nil {
				specificChannelID = ch.ID
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if specificChannelID != "" {
		d, ok := h.svc.GetChannelDuration(specificChannelID)
		if !ok {
			h.respondMessage(s, i, fmt.Sprintf("Channel <#%s> is **not** configured as a Flash & Dash channel.", specificChannelID))
			return
		}

		embed := &discordgo.MessageEmbed{
			Title:       "⚡ Flash & Dash Status",
			Description: fmt.Sprintf("Configuration for <#%s>:", specificChannelID),
			Color:       0x5865F2,
			Fields: []*discordgo.MessageEmbedField{
				{
					Name:   "Deletion Timer",
					Value:  fmt.Sprintf("`%s` (%d seconds)", d.String(), int(d.Seconds())),
					Inline: true,
				},
				{
					Name:   "Auto-Spoilering",
					Value:  "✅ Active",
					Inline: true,
				},
			},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{embed},
			},
		})
		return
	}

	// List all channels
	configs, err := h.svc.ListChannels(ctx, guildID)
	if err != nil {
		h.logger.Error("failed to list flash channels", "error", err)
		h.respondError(s, i, "Failed to retrieve Flash & Dash channels from database.")
		return
	}

	if len(configs) == 0 {
		h.respondMessage(s, i, "No Flash & Dash channels are currently configured in this server. Use `/flash set-timer` to enable one.")
		return
	}

	var sb strings.Builder
	for _, c := range configs {
		sb.WriteString(fmt.Sprintf("• <#%s>: Timer `%s` (%d sec)\n", c.ChannelID, c.Duration().String(), c.DurationSec))
	}

	embed := &discordgo.MessageEmbed{
		Title:       "⚡ Active Flash & Dash Channels",
		Description: sb.String(),
		Color:       0x5865F2,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("%d channel(s) active", len(configs)),
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

// HandleMessageCreate intercepts messages in Flash and Dash channels:
// 1. Enforces photo-only policy: immediately deletes text-only comments or non-photo attachments.
// 2. If photos are unspoilered, downloads them, re-posts with SPOILER_ prefix (preserving optional caption),
//    deletes original, and schedules deletion.
// 3. If all photos are already spoilered, schedules deletion directly.
func (h *FlashHandler) HandleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil || m.Author.Bot {
		return
	}

	if h.cfg.GuildID != "" && m.GuildID != "" && m.GuildID != h.cfg.GuildID {
		return
	}

	duration, isFlash := h.svc.GetChannelDuration(m.ChannelID)
	if !isFlash {
		return
	}

	// Enforce photo-only policy: reject text-only comments or non-photo attachments immediately
	if len(m.Attachments) == 0 || !hasOnlyPhotoAttachments(m.Attachments) {
		if err := s.ChannelMessageDelete(m.ChannelID, m.ID); err != nil {
			h.logger.Warn("failed to delete non-photo message in flash and dash channel",
				"channel_id", m.ChannelID,
				"message_id", m.ID,
				"author_id", m.Author.ID,
				"error", err,
			)
		} else {
			h.logger.Info("deleted non-photo comment/message in flash and dash channel",
				"channel_id", m.ChannelID,
				"message_id", m.ID,
				"author_id", m.Author.ID,
			)
		}
		return
	}

	hasUnspoilered := false
	for _, att := range m.Attachments {
		if !strings.HasPrefix(att.Filename, "SPOILER_") {
			hasUnspoilered = true
			break
		}
	}

	if hasUnspoilered {
		// Download, re-upload as spoiler, delete original, and schedule deletion
		go h.autoSpoilerAndSchedule(s, m, duration)
		return
	}

	// Already spoilered photo attachments: schedule deletion
	h.svc.ScheduleMessageDeletion(s, m.GuildID, m.ChannelID, m.ID, duration)
}

// IsPhotoAttachment checks whether a Discord message attachment is an image/photo file.
func IsPhotoAttachment(att *discordgo.MessageAttachment) bool {
	if att == nil {
		return false
	}
	ct := strings.ToLower(strings.TrimSpace(att.ContentType))
	if strings.HasPrefix(ct, "image/") {
		return true
	}
	cleanName := strings.ToLower(strings.TrimPrefix(att.Filename, "SPOILER_"))
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".heic", ".avif"} {
		if strings.HasSuffix(cleanName, ext) {
			return true
		}
	}
	return false
}

func hasOnlyPhotoAttachments(attachments []*discordgo.MessageAttachment) bool {
	if len(attachments) == 0 {
		return false
	}
	for _, att := range attachments {
		if !IsPhotoAttachment(att) {
			return false
		}
	}
	return true
}

func (h *FlashHandler) autoSpoilerAndSchedule(s *discordgo.Session, m *discordgo.MessageCreate, duration time.Duration) {
	var files []*discordgo.File

	for _, att := range m.Attachments {
		fileBytes, err := h.downloadAttachment(att.URL)
		if err != nil {
			h.logger.Error("failed to download attachment for auto-spoilering",
				"channel_id", m.ChannelID,
				"message_id", m.ID,
				"url", att.URL,
				"error", err,
			)
			continue
		}

		filename := att.Filename
		if !strings.HasPrefix(filename, "SPOILER_") {
			filename = "SPOILER_" + filename
		}

		files = append(files, &discordgo.File{
			Name:        filename,
			ContentType: att.ContentType,
			Reader:      bytes.NewReader(fileBytes),
		})
	}

	if len(files) == 0 {
		// If downloading all files failed, schedule original message for deletion
		h.svc.ScheduleMessageDeletion(s, m.GuildID, m.ChannelID, m.ID, duration)
		return
	}

	// Delete the original unspoilered message
	if err := s.ChannelMessageDelete(m.ChannelID, m.ID); err != nil {
		h.logger.Warn("failed to delete unspoilered original message",
			"channel_id", m.ChannelID,
			"message_id", m.ID,
			"error", err,
		)
	}

	// Re-post with SPOILER tags and author attribution
	var contentBuilder strings.Builder
	contentBuilder.WriteString(fmt.Sprintf("📸 **Photo from <@%s>** *(Auto-spoilered)*", m.Author.ID))
	if trimmed := strings.TrimSpace(m.Content); trimmed != "" {
		contentBuilder.WriteString("\n")
		contentBuilder.WriteString(trimmed)
	}

	msgSend := &discordgo.MessageSend{
		Content: contentBuilder.String(),
		Files:   files,
	}

	repostedMsg, err := s.ChannelMessageSendComplex(m.ChannelID, msgSend)
	if err != nil {
		h.logger.Error("failed to send auto-spoilered message to channel",
			"channel_id", m.ChannelID,
			"author_id", m.Author.ID,
			"error", err,
		)
		return
	}

	// Schedule the re-posted spoilered message for deletion
	h.svc.ScheduleMessageDeletion(s, m.GuildID, m.ChannelID, repostedMsg.ID, duration)
}

func (h *FlashHandler) downloadAttachment(url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d from attachment download", resp.StatusCode)
	}

	// Limit attachment size to 25MB (Discord maximum standard upload)
	limitedReader := io.LimitReader(resp.Body, 25*1024*1024)
	return io.ReadAll(limitedReader)
}

func (h *FlashHandler) respondError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "❌ " + message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *FlashHandler) respondMessage(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// ParseFlashDuration parses flexible duration strings such as "30s", "5m", "2h", "1d", or raw seconds "60".
func ParseFlashDuration(raw string) (time.Duration, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return 0, fmt.Errorf("duration cannot be empty")
	}

	// Check if pure numeric seconds
	if sec, err := strconv.Atoi(trimmed); err == nil {
		if sec < 10 {
			return 0, fmt.Errorf("duration must be at least 10 seconds")
		}
		return time.Duration(sec) * time.Second, nil
	}

	// Parse days format (e.g. 1d, 2d)
	if strings.HasSuffix(trimmed, "d") {
		daysStr := strings.TrimSuffix(trimmed, "d")
		days, err := strconv.Atoi(daysStr)
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid days format %q", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("cannot parse duration %q: %w", raw, err)
	}

	if d < 10*time.Second {
		return 0, fmt.Errorf("duration must be at least 10 seconds")
	}

	return d, nil
}