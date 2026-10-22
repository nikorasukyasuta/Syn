package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/domain"
)

const (
	// RedAnnouncementTitle is the header of the official safeword stop message.
	RedAnnouncementTitle = "🛑 RED — STOP 🛑"

	// RedAnnouncementText is the exact verbatim declaration required by the community.
	RedAnnouncementText = "Please stop the current conversation and change the subject.\n\n" +
		"Mods/Admins have been pinged to check in with everyone involved. We understand Discord can lag and messages may cross over, but intentionally continuing after RED may result in a timeout.\n\n" +
		"Respect RED. Give everyone space."

	// RedAnnouncementColor is Discord's official danger red hex color.
	RedAnnouncementColor = 0xED4245

	// SafewordSlowmodeDelay is the rate limit delay per user applied to the channel.
	SafewordSlowmodeDelay = 30

	// SafewordDuration is the length of time the 30s slowmode remains active before automatic reset.
	SafewordDuration = 5 * time.Minute
)

// SafewordHandler coordinates the /red slash command, 🛑 reaction trigger,
// staff alerts with direct message links, and 5-minute 30s slowmode enforcement.
type SafewordHandler struct {
	staffRoleIDs        []string
	staffAlertChannelID string
	safewordEmoji       string
	auditRepo           domain.AuditRepository
	logger              *slog.Logger

	mu           sync.Mutex
	activeTimers map[string]*time.Timer
	lastTrigger  map[string]time.Time
	redMsgIDs    map[string]struct{}
}

// NewSafewordHandler initializes a new SafewordHandler instance.
func NewSafewordHandler(
	cfg *config.Config,
	auditRepo domain.AuditRepository,
	logger *slog.Logger,
) *SafewordHandler {
	emoji := cfg.SafewordEmoji
	if emoji == "" {
		emoji = "🛑"
	}
	return &SafewordHandler{
		staffRoleIDs:        cfg.StaffRoleIDs,
		staffAlertChannelID: strings.TrimSpace(cfg.StaffAlertChannelID),
		safewordEmoji:       emoji,
		auditRepo:           auditRepo,
		logger:              logger,
		activeTimers:        make(map[string]*time.Timer),
		lastTrigger:         make(map[string]time.Time),
		redMsgIDs:           make(map[string]struct{}),
	}
}

// RedCommandDefinition defines the /red slash command runnable by any server member.
func RedCommandDefinition() *discordgo.ApplicationCommand {
	dmPerm := false

	return &discordgo.ApplicationCommand{
		Name:         "red",
		Description:  "Trigger an emergency RED safeword alert to halt conversation and alert staff",
		DMPermission: &dmPerm,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "reason",
				Description: "Optional context or reason for staff (confidential)",
				Required:    false,
			},
		},
	}
}

// ClearSlowmodeCommandDefinition defines the staff-only command to remove safeword slowmode.
func ClearSlowmodeCommandDefinition() *discordgo.ApplicationCommand {
	var defaultPerms int64 = 0
	dmPerm := false

	return &discordgo.ApplicationCommand{
		Name:                     "clear-slowmode",
		Description:              "[Staff Only] Clear safeword slowmode and restore normal chat rate limit",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPerm,
	}
}

// TriggerRed executes the core safeword logic:
// 1. Posts the official RED declaration in the channel.
// 2. Alerts staff with a direct jump link to the posted declaration.
// 3. Puts the channel into 30s slowmode for 5 minutes.
// 4. Schedules automatic slowmode reset after 5 minutes.
func (h *SafewordHandler) TriggerRed(
	s *discordgo.Session,
	guildID, channelID, actorID, triggerSource, reason string,
) (*discordgo.Message, error) {
	if channelID == "" {
		return nil, fmt.Errorf("channelID cannot be empty")
	}

	h.mu.Lock()
	// Debounce: prevent duplicate bot broadcasts if triggered within 15 seconds in the same channel
	if last, exists := h.lastTrigger[channelID]; exists && time.Since(last) < 15*time.Second {
		h.mu.Unlock()
		h.logger.Warn("safeword: red triggered again during cooldown, skipping duplicate broadcast",
			"channel_id", channelID,
			"actor_id", actorID,
			"trigger_source", triggerSource,
		)
		return nil, nil
	}
	h.lastTrigger[channelID] = time.Now()
	h.mu.Unlock()

	h.logger.Info("safeword: triggering RED alert",
		"guild_id", guildID,
		"channel_id", channelID,
		"actor_id", actorID,
		"trigger_source", triggerSource,
	)

	// Build staff role mentions
	var staffMentions []string
	for _, rid := range h.staffRoleIDs {
		staffMentions = append(staffMentions, fmt.Sprintf("<@&%s>", rid))
	}
	pingsStr := strings.Join(staffMentions, " ")

	// 1. Post the official RED declaration in the target channel
	redEmbed := &discordgo.MessageEmbed{
		Title:       RedAnnouncementTitle,
		Description: RedAnnouncementText,
		Color:       RedAnnouncementColor,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Channel slowmode set to 30s for the next 5 minutes",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	msgSend := &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{redEmbed},
	}
	// If no dedicated staff alert channel is configured, ping staff directly in this channel
	if h.staffAlertChannelID == "" && len(staffMentions) > 0 {
		msgSend.Content = pingsStr
	}

	redMsg, err := s.ChannelMessageSendComplex(channelID, msgSend)
	if err != nil {
		h.logger.Error("safeword: failed to send RED declaration message", "error", err, "channel_id", channelID)
		return nil, err
	}

	// Record the bot's message ID to avoid self-reaction loops
	h.mu.Lock()
	h.redMsgIDs[redMsg.ID] = struct{}{}
	h.mu.Unlock()

	messageLink := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, redMsg.ID)

	// 2. Alert Staff Members with direct link to the RED message
	if h.staffAlertChannelID != "" {
		staffAlertEmbed := &discordgo.MessageEmbed{
			Title: "🚨 Staff Alert: Safeword (RED) Used",
			Description: fmt.Sprintf(
				"A safeword was used in <#%s>.\n\n"+
					"**[🛑 Click Here to Jump to the RED Message](%s)**\n\n"+
					"• **Channel:** <#%s>\n"+
					"• **Triggered by:** <@%s> (%s)\n"+
					"• **Status:** Channel placed into 30s slowmode for 5 minutes.\n"+
					"• **Staff Action:** Check in with everyone involved. Use `/clear-slowmode` in the channel when resolved.",
				channelID, messageLink, channelID, actorID, triggerSource,
			),
			Color:     RedAnnouncementColor,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		if reason != "" {
			staffAlertEmbed.Fields = []*discordgo.MessageEmbedField{
				{
					Name:  "User Note / Reason",
					Value: reason,
				},
			}
		}

		_, err = s.ChannelMessageSendComplex(h.staffAlertChannelID, &discordgo.MessageSend{
			Content: pingsStr,
			Embeds:  []*discordgo.MessageEmbed{staffAlertEmbed},
		})
		if err != nil {
			h.logger.Error("safeword: failed to send staff alert to staff channel", "error", err, "staff_channel", h.staffAlertChannelID)
		} else {
			h.logger.Info("safeword: staff alert sent to staff channel", "staff_channel", h.staffAlertChannelID, "message_link", messageLink)
		}
	} else {
		h.logger.Info("safeword: staff alerted in-channel", "channel_id", channelID, "message_link", messageLink)
	}

	// 3. Place the channel into 30s slowmode
	slowmodeVal := SafewordSlowmodeDelay
	_, err = s.ChannelEdit(channelID, &discordgo.ChannelEdit{
		RateLimitPerUser: &slowmodeVal,
	})
	if err != nil {
		h.logger.Error("safeword: failed to set channel slowmode (check bot 'Manage Channels' permission)",
			"error", err,
			"channel_id", channelID,
		)
	} else {
		h.logger.Info("safeword: channel slowmode set to 30s", "channel_id", channelID)
	}

	// 4. Schedule automatic slowmode reset after 5 minutes
	h.mu.Lock()
	if oldTimer, ok := h.activeTimers[channelID]; ok && oldTimer != nil {
		oldTimer.Stop()
	}
	h.activeTimers[channelID] = time.AfterFunc(SafewordDuration, func() {
		h.autoResetSlowmode(s, guildID, channelID)
	})
	h.mu.Unlock()

	// 5. Immutable database audit log
	if h.auditRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.auditRepo.LogAudit(ctx, actorID, channelID, "SAFEWORD_RED_TRIGGERED")
	}

	return redMsg, nil
}

// ClearSlowmode resets channel slowmode to 0 and cancels any pending auto-reset timer.
func (h *SafewordHandler) ClearSlowmode(s *discordgo.Session, guildID, channelID, actorID string) error {
	h.mu.Lock()
	if timer, ok := h.activeTimers[channelID]; ok && timer != nil {
		timer.Stop()
		delete(h.activeTimers, channelID)
	}
	h.mu.Unlock()

	zero := 0
	_, err := s.ChannelEdit(channelID, &discordgo.ChannelEdit{
		RateLimitPerUser: &zero,
	})
	if err != nil {
		h.logger.Error("safeword: failed to clear channel slowmode", "error", err, "channel_id", channelID)
		return err
	}

	h.logger.Info("safeword: slowmode cleared manually by staff", "channel_id", channelID, "actor_id", actorID)

	if h.auditRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.auditRepo.LogAudit(ctx, actorID, channelID, "SAFEWORD_SLOWMODE_CLEARED")
	}

	_, _ = s.ChannelMessageSend(channelID, fmt.Sprintf("✅ Slowmode has been cleared by <@%s>. Normal channel rate limit restored.", actorID))
	return nil
}

func (h *SafewordHandler) autoResetSlowmode(s *discordgo.Session, guildID, channelID string) {
	h.mu.Lock()
	delete(h.activeTimers, channelID)
	h.mu.Unlock()

	zero := 0
	_, err := s.ChannelEdit(channelID, &discordgo.ChannelEdit{
		RateLimitPerUser: &zero,
	})
	if err != nil {
		h.logger.Error("safeword: failed to auto-reset channel slowmode", "error", err, "channel_id", channelID)
		return
	}

	h.logger.Info("safeword: 5-minute timer expired, slowmode reset to 0", "channel_id", channelID)
	_, _ = s.ChannelMessageSend(channelID, "ℹ️ Safeword slowmode period (5 minutes) has ended. Slowmode has been reset to normal.")
}

// HandleRedCommand handles /red slash commands by any user.
func (h *SafewordHandler) HandleRedCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var reason string
	if data, ok := i.Data.(discordgo.ApplicationCommandInteractionData); ok {
		for _, opt := range data.Options {
			if opt.Name == "reason" {
				reason = strings.TrimSpace(opt.StringValue())
			}
		}
	}

	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	// Immediately respond ephemerally to the caller to satisfy Discord's 3-second timeout
	respondEphemeral(s, i, "🛑 **RED safeword triggered.** Staff has been alerted and 30s slowmode enabled.")

	_, err := h.TriggerRed(s, i.GuildID, i.ChannelID, userID, "slash_command", reason)
	if err != nil {
		h.logger.Error("safeword: error executing /red command", "error", err, "channel_id", i.ChannelID)
	}
}

// HandleClearSlowmodeCommand handles /clear-slowmode slash commands by staff.
func (h *SafewordHandler) HandleClearSlowmodeCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	err := h.ClearSlowmode(s, i.GuildID, i.ChannelID, userID)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to clear slowmode: %v (Ensure bot has 'Manage Channels' permission).", err))
		return
	}

	respondEphemeral(s, i, "✅ Slowmode cleared in this channel.")
}

// HandleReactionAdd listens for reaction events and triggers RED if the reaction is the stop sign emoji.
func (h *SafewordHandler) HandleReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	if r.GuildID == "" || (s.State != nil && s.State.User != nil && r.UserID == s.State.User.ID) {
		return
	}

	emoji := r.Emoji.Name
	apiName := r.Emoji.APIName()
	if emoji != h.safewordEmoji && apiName != h.safewordEmoji {
		return
	}

	// Prevent reaction loops on the RED announcement message itself
	h.mu.Lock()
	if _, isRedMsg := h.redMsgIDs[r.MessageID]; isRedMsg {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()

	h.logger.Info("safeword: reaction emoji detected",
		"guild_id", r.GuildID,
		"channel_id", r.ChannelID,
		"message_id", r.MessageID,
		"user_id", r.UserID,
		"emoji", emoji,
	)

	triggerSource := fmt.Sprintf("reaction_%s_on_message_%s", emoji, r.MessageID)
	_, err := h.TriggerRed(s, r.GuildID, r.ChannelID, r.UserID, triggerSource, "")
	if err != nil {
		h.logger.Error("safeword: error executing RED via reaction", "error", err, "channel_id", r.ChannelID)
	}
}

