package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/cache"
	"syn/internal/discord/syncutil"
	"syn/internal/domain"
	"syn/internal/service"
)

// HoneypotReleaseButtonPrefix is the custom ID prefix for the interactive staff release button.
const HoneypotReleaseButtonPrefix = "btn_honeypot_release:"

// HoneypotChannelTopic is the blatant warning topic set on the honeypot channel.
const HoneypotChannelTopic = "⛔ AUTOMATED BOT SPAM TRAP — DO NOT TYPE IN THIS CHANNEL! Typing anything here immediately quarantines your account with the Spammer role and automatically kicks you after 3 days."

// HoneypotCommandDefinition returns the slash command specification for /honeypot (Staff Only).
func HoneypotCommandDefinition() *discordgo.ApplicationCommand {
	var defaultPerms int64 = 0
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:                     "honeypot",
		Description:              "[Staff Only] Manage the Honeypot spam-trap channel and Spammer quarantines",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "release",
				Description: "Remove a user from Spammer quarantine, restore their roles, and cancel the 3-day auto-kick",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "user",
						Description: "The quarantined member to release",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    true,
					},
				},
			},
			{
				Name:        "setup",
				Description: "Provision or re-sync the Honeypot spam-trap channel, Spammer role, and blatant warning notice",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "channel",
						Description: "Existing channel to use as the Honeypot (omit to auto-create #⛔-do-not-type-here)",
						Type:        discordgo.ApplicationCommandOptionChannel,
						ChannelTypes: []discordgo.ChannelType{
							discordgo.ChannelTypeGuildText,
						},
						Required: false,
					},
				},
			},
			{
				Name:        "list",
				Description: "List all users currently in Spammer quarantine and their 3-day auto-kick deadlines",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "status",
				Description: "View Honeypot channel configuration, Spammer role, and active quarantine summary",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	}
}

// UnquarantineCommandDefinition returns the standalone /unquarantine slash command for Staff convenience.
func UnquarantineCommandDefinition() *discordgo.ApplicationCommand {
	var defaultPerms int64 = 0
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:                     "unquarantine",
		Description:              "[Staff Only] Remove a user from Honeypot Spammer quarantine and restore their roles",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "The quarantined member to release from Spammer quarantine",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	}
}

// HoneypotHandler coordinates Discord events, staff commands, and interactive buttons
// for the Honeypot spam-trap channel and 3-day Spammer quarantine system.
type HoneypotHandler struct {
	svc     *service.HoneypotService
	cfg     *config.Config
	cache   *cache.ResourceCache
	lockMgr *syncutil.UserLockManager
	logger  *slog.Logger
	session *discordgo.Session
}

// NewHoneypotHandler constructs a new HoneypotHandler.
func NewHoneypotHandler(
	svc *service.HoneypotService,
	cfg *config.Config,
	resCache *cache.ResourceCache,
	lockMgr *syncutil.UserLockManager,
	logger *slog.Logger,
) *HoneypotHandler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &HoneypotHandler{
		svc:     svc,
		cfg:     cfg,
		cache:   resCache,
		lockMgr: lockMgr,
		logger:  logger,
	}
	if svc != nil {
		svc.SetOutcomeHook(h.handleQuarantineOutcome)
	}
	return h
}

// AttachSession binds the live Discord session for background auto-kick notifications.
func (h *HoneypotHandler) AttachSession(s *discordgo.Session) {
	h.session = s
}

// HandleHoneypotCommand routes /honeypot subcommands (setup, release, list, status).
func (h *HoneypotHandler) HandleHoneypotCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}
	cmdData := i.ApplicationCommandData()
	options := cmdData.Options
	if len(options) == 0 {
		respondEphemeral(s, i, "❌ Please specify a subcommand: `release`, `setup`, `list`, or `status`.")
		return
	}

	sub := options[0]
	switch sub.Name {
	case "release":
		var targetUser *discordgo.User
		for _, opt := range sub.Options {
			if opt.Name == "user" {
				targetUser = resolveTargetUser(s, cmdData, opt)
			}
		}
		h.executeRelease(s, i, targetUser)
	case "setup":
		var explicitChannelID string
		for _, opt := range sub.Options {
			if opt.Name == "channel" {
				if ch := opt.ChannelValue(s); ch != nil {
					explicitChannelID = ch.ID
				}
			}
		}
		h.handleSetup(s, i, explicitChannelID)
	case "list":
		h.handleList(s, i)
	case "status":
		h.handleStatus(s, i)
	default:
		respondEphemeral(s, i, fmt.Sprintf("❌ Unknown subcommand: `%s`", sub.Name))
	}
}

// HandleUnquarantineCommand processes the standalone /unquarantine <user> command for Staff.
func (h *HoneypotHandler) HandleUnquarantineCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}
	cmdData := i.ApplicationCommandData()
	var targetUser *discordgo.User
	for _, opt := range cmdData.Options {
		if opt.Name == "user" {
			targetUser = resolveTargetUser(s, cmdData, opt)
		}
	}
	h.executeRelease(s, i, targetUser)
}

func resolveTargetUser(
	s *discordgo.Session,
	data discordgo.ApplicationCommandInteractionData,
	opt *discordgo.ApplicationCommandInteractionDataOption,
) *discordgo.User {
	if opt == nil {
		return nil
	}
	if userID, ok := opt.Value.(string); ok && userID != "" {
		if data.Resolved != nil && data.Resolved.Users != nil {
			if u, exists := data.Resolved.Users[userID]; exists && u != nil {
				return u
			}
		}
	}
	return opt.UserValue(s)
}

func (h *HoneypotHandler) executeRelease(s *discordgo.Session, i *discordgo.InteractionCreate, targetUser *discordgo.User) {
	if targetUser == nil {
		respondEphemeral(s, i, "❌ Could not resolve the target member.")
		return
	}

	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	actorID := ""
	if i.Member != nil && i.Member.User != nil {
		actorID = i.Member.User.ID
	} else if i.User != nil {
		actorID = i.User.ID
	}

	if h.lockMgr != nil {
		unlock := h.lockMgr.Lock(targetUser.ID)
		defer unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q, err := h.svc.Release(ctx, guildID, targetUser.ID, actorID)
	if errors.Is(err, domain.ErrNotQuarantined) {
		respondEphemeral(s, i, fmt.Sprintf("ℹ️ <@%s> is not currently in active Honeypot quarantine (any stray `Spammer` role was removed if present).", targetUser.ID))
		return
	}

	restoredCount := 0
	if q != nil {
		restoredCount = len(q.RemovedRoleIDs)
	}

	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("⚠️ Released <@%s> from Spammer quarantine and cancelled their 3-day auto-kick timer, but some roles could not be restored automatically: `%v`", targetUser.ID, err))
		return
	}

	respondEphemeral(s, i, fmt.Sprintf("✅ **Quarantine Removed:** <@%s> has been released from the **Spammer** quarantine, %d previous role(s) were restored, and the 3-day auto-kick timer was cancelled.", targetUser.ID, restoredCount))
	h.sendStaffReleaseNotice(s, q, targetUser.ID, actorID)
}

func (h *HoneypotHandler) handleSetup(s *discordgo.Session, i *discordgo.InteractionCreate, explicitChannelID string) {
	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	chID, roleID, warnMsgID, err := h.EnsureHoneypot(ctx, s, guildID, explicitChannelID)
	if err != nil {
		h.logger.Error("failed to provision honeypot via /honeypot setup", "error", err)
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to configure Honeypot: %v", err))
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🍯 Honeypot Spam Trap Configured",
		Description: "The Honeypot channel and **Spammer** quarantine role are active and synchronized.",
		Color:       0xED4245,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Honeypot Channel",
				Value:  fmt.Sprintf("<#%s>", chID),
				Inline: true,
			},
			{
				Name:   "Quarantine Role",
				Value:  fmt.Sprintf("<@&%s>", roleID),
				Inline: true,
			},
			{
				Name:   "Auto-Kick Timer",
				Value:  fmt.Sprintf("`%s` (3 Days)", h.svc.KickDelay().String()),
				Inline: true,
			},
			{
				Name:   "Warning Notice",
				Value:  fmt.Sprintf("Message ID `%s` (Pinned in <#%s>)", warnMsgID, chID),
				Inline: false,
			},
			{
				Name:   "Staff Release Commands",
				Value:  "`/unquarantine user:@member` or `/honeypot release user:@member`",
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Moderation Suite • Honeypot Anti-Spam",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *HoneypotHandler) handleList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	list := h.svc.ActiveQuarantines()
	if len(list) == 0 {
		respondEphemeral(s, i, "✅ No members are currently in Honeypot Spammer quarantine.")
		return
	}

	var sb strings.Builder
	for idx, q := range list {
		if idx >= 25 {
			sb.WriteString(fmt.Sprintf("\n*...and %d more quarantined account(s)*", len(list)-idx))
			break
		}
		sb.WriteString(fmt.Sprintf(
			"• <@%s> (`%s`) — Quarantined <t:%d:R> • **Auto-Kick:** <t:%d:R> (%d role(s) stripped)\n",
			q.UserID,
			q.Username,
			q.QuarantinedAt.Unix(),
			q.KickAt.Unix(),
			len(q.RemovedRoleIDs),
		))
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("🔒 Active Spammer Quarantines (%d)", len(list)),
		Description: sb.String(),
		Color:       0xED4245,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Use /unquarantine <user> or /honeypot release <user> to release a member",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *HoneypotHandler) handleStatus(s *discordgo.Session, i *discordgo.InteractionCreate) {
	hpCfg := h.svc.Config()
	activeCount := len(h.svc.ActiveQuarantines())

	chDisplay := "*Not configured (run `/honeypot setup`)*"
	if hpCfg.ChannelID != "" {
		chDisplay = fmt.Sprintf("<#%s> (`%s`)", hpCfg.ChannelID, hpCfg.ChannelID)
	}

	roleDisplay := "*Not configured (run `/honeypot setup`)*"
	if hpCfg.SpammerRoleID != "" {
		roleDisplay = fmt.Sprintf("<@&%s> (`%s`)", hpCfg.SpammerRoleID, hpCfg.SpammerRoleID)
	}

	embed := &discordgo.MessageEmbed{
		Title: "🍯 Honeypot Anti-Spam Status",
		Color: 0x5865F2,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Honeypot Channel",
				Value:  chDisplay,
				Inline: true,
			},
			{
				Name:   "Spammer Role",
				Value:  roleDisplay,
				Inline: true,
			},
			{
				Name:   "Active Quarantines",
				Value:  fmt.Sprintf("`%d` member(s)", activeCount),
				Inline: true,
			},
			{
				Name:   "Auto-Kick Deadline",
				Value:  "3 Days (`72h`) if not removed by Staff",
				Inline: true,
			},
			{
				Name:   "Staff Release Commands",
				Value:  "`/unquarantine <user>` or `/honeypot release <user>`",
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
}

// HandleComponentInteraction processes the interactive [Release from Quarantine] button on staff alert messages.
func (h *HoneypotHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i == nil || i.Interaction == nil || i.Type != discordgo.InteractionMessageComponent {
		return false
	}
	customID := i.MessageComponentData().CustomID
	if !strings.HasPrefix(customID, HoneypotReleaseButtonPrefix) {
		return false
	}

	if !h.isStaffMember(i.Member) {
		respondEphemeral(s, i, "🚫 Only Server Staff can release members from Honeypot Spammer quarantine.")
		return true
	}

	targetUserID := strings.TrimSpace(strings.TrimPrefix(customID, HoneypotReleaseButtonPrefix))
	if targetUserID == "" {
		respondEphemeral(s, i, "❌ Invalid quarantine release button payload.")
		return true
	}

	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	actorID := ""
	if i.Member != nil && i.Member.User != nil {
		actorID = i.Member.User.ID
	}

	if h.lockMgr != nil {
		unlock := h.lockMgr.Lock(targetUserID)
		defer unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q, err := h.svc.Release(ctx, guildID, targetUserID, actorID)
	if errors.Is(err, domain.ErrNotQuarantined) {
		respondEphemeral(s, i, fmt.Sprintf("ℹ️ <@%s> is already released from quarantine.", targetUserID))
		return true
	}

	// Update the staff alert message button to show it was resolved
	if i.Message != nil {
		disabledBtn := []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						CustomID: customID,
						Label:    "Released from Quarantine",
						Style:    discordgo.SecondaryButton,
						Disabled: true,
						Emoji:    &discordgo.ComponentEmoji{Name: "✅"},
					},
				},
			},
		}
		_, _ = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
			ID:         i.Message.ID,
			Channel:    i.ChannelID,
			Embeds:     &i.Message.Embeds,
			Components: &disabledBtn,
		})
	}

	restoredCount := 0
	if q != nil {
		restoredCount = len(q.RemovedRoleIDs)
	}

	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("⚠️ Released <@%s> from quarantine and cancelled the 3-day auto-kick, but some roles could not be restored: `%v`", targetUserID, err))
		return true
	}

	respondEphemeral(s, i, fmt.Sprintf("✅ Released <@%s> from Spammer quarantine, restored %d role(s), and cancelled the 3-day auto-kick timer.", targetUserID, restoredCount))
	h.sendStaffReleaseNotice(s, q, targetUserID, actorID)
	return true
}

// HandleMessageCreate intercepts messages posted in the Honeypot channel:
// 1. Ignores bots.
// 2. Deletes messages immediately so the Honeypot channel stays clean with only the blatant warning.
// 3. Exempts Server Staff from being quarantined if they accidentally post in the channel.
// 4. Quarantines any non-staff poster with the Spammer role, strips & saves their previous roles,
//    starts the 3-day (72h) auto-kick timer, DMs the user, and alerts Server Staff.
func (h *HoneypotHandler) HandleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil || m.Author.Bot {
		return
	}
	if h.svc == nil || !h.svc.IsHoneypotChannel(m.ChannelID) {
		return
	}

	guildID := m.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}
	if h.cfg != nil && h.cfg.GuildID != "" && guildID != "" && guildID != h.cfg.GuildID {
		return
	}

	// Capture content snippet before deleting the message
	msgPreview := strings.TrimSpace(m.Content)
	if len(m.Attachments) > 0 {
		if msgPreview != "" {
			msgPreview += fmt.Sprintf(" [+%d attachment(s)]", len(m.Attachments))
		} else {
			msgPreview = fmt.Sprintf("[%d attachment(s)]", len(m.Attachments))
		}
	}
	if len([]rune(msgPreview)) > 300 {
		msgPreview = string([]rune(msgPreview)[:300]) + "..."
	}
	if msgPreview == "" {
		msgPreview = "*[Empty / sticker / embed message]*"
	}

	// Always delete the message in the honeypot channel immediately
	if err := s.ChannelMessageDelete(m.ChannelID, m.ID); err != nil {
		h.logger.Warn("failed to delete message in honeypot channel",
			"channel_id", m.ChannelID,
			"message_id", m.ID,
			"author_id", m.Author.ID,
			"error", err,
		)
	}

	// Exempt Server Staff from being quarantined if they accidentally type in the honeypot channel
	if h.isStaffMember(m.Member) {
		h.logger.Info("ignored staff message in honeypot channel (deleted message without quarantining)",
			"channel_id", m.ChannelID,
			"user_id", m.Author.ID,
			"username", m.Author.Username,
		)
		return
	}

	if h.lockMgr != nil {
		unlock := h.lockMgr.Lock(m.Author.ID)
		defer unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	q, created, err := h.svc.Quarantine(ctx, guildID, m.Author.ID, m.Author.Username, m.ChannelID, m.ID)
	if err != nil {
		h.logger.Error("failed to quarantine honeypot poster",
			"guild_id", guildID,
			"user_id", m.Author.ID,
			"username", m.Author.Username,
			"error", err,
		)
		return
	}
	if !created || q == nil {
		return
	}

	h.sendQuarantineDM(s, m.Author.ID, m.ChannelID, q)
	h.sendStaffQuarantineAlert(s, q, msgPreview)

	// Clean up any other spam messages posted by this user across the server within the last 5 minutes,
	// while leaving all other users' messages intact.
	go h.PurgeRecentUserMessages(s, guildID, m.Author.ID, 5*time.Minute)
}

// PurgeRecentUserMessages scans writable text channels in the guild and deletes messages
// authored by targetUserID sent within window (5 minutes), leaving all other messages untouched.
func (h *HoneypotHandler) PurgeRecentUserMessages(s *discordgo.Session, guildID, targetUserID string, window time.Duration) {
	if s == nil || guildID == "" || targetUserID == "" || window <= 0 {
		return
	}

	channels, err := s.GuildChannels(guildID)
	if err != nil {
		h.logger.Warn("failed to fetch guild channels for honeypot message purge", "guild_id", guildID, "error", err)
		return
	}

	cutoff := time.Now().UTC().Add(-window)
	deletedTotal := 0

	for _, ch := range channels {
		if ch == nil {
			continue
		}
		// Only check text-based channels
		if ch.Type != discordgo.ChannelTypeGuildText &&
			ch.Type != discordgo.ChannelTypeGuildNews &&
			ch.Type != discordgo.ChannelTypeGuildPublicThread &&
			ch.Type != discordgo.ChannelTypeGuildPrivateThread {
			continue
		}

		msgs, err := s.ChannelMessages(ch.ID, 100, "", "", "")
		if err != nil {
			continue
		}

		var toDelete []string
		for _, msg := range msgs {
			if msg == nil || msg.Author == nil || msg.Author.ID != targetUserID {
				continue
			}

			// If timestamp is before cutoff (older than 5 minutes), skip
			if !msg.Timestamp.IsZero() && msg.Timestamp.Before(cutoff) {
				continue
			}

			toDelete = append(toDelete, msg.ID)
		}

		if len(toDelete) == 0 {
			continue
		}

		if len(toDelete) == 1 {
			if err := s.ChannelMessageDelete(ch.ID, toDelete[0]); err == nil {
				deletedTotal++
			}
		} else {
			if err := s.ChannelMessagesBulkDelete(ch.ID, toDelete); err == nil {
				deletedTotal += len(toDelete)
			} else {
				// Fallback to individual deletion if bulk delete fails
				for _, msgID := range toDelete {
					if dErr := s.ChannelMessageDelete(ch.ID, msgID); dErr == nil {
						deletedTotal++
					}
				}
			}
		}
	}

	if deletedTotal > 0 {
		h.logger.Info("purged recent spam messages from quarantined user",
			"guild_id", guildID,
			"user_id", targetUserID,
			"deleted_count", deletedTotal,
			"window", window.String(),
		)
	}
}

// EnsureHoneypot provisions or synchronizes:
// 1. The Spammer quarantine role ("Spammer").
// 2. The Honeypot channel ("#⛔-do-not-type-here" or explicitChannelID) with blatant topic & overwrites.
// 3. Channel-level deny overwrites for the Spammer role across standard server channels.
// 4. The pinned blatant warning message and embed inside the Honeypot channel.
func (h *HoneypotHandler) EnsureHoneypot(
	ctx context.Context,
	s *discordgo.Session,
	guildID string,
	explicitChannelID string,
) (string, string, string, error) {
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}
	h.AttachSession(s)

	roleID, err := h.resolveOrCreateSpammerRole(s, guildID)
	if err != nil {
		return "", "", "", err
	}

	channelID, err := h.resolveOrCreateHoneypotChannel(s, guildID, roleID, explicitChannelID)
	if err != nil {
		return "", roleID, "", err
	}

	// Restrict the Spammer role from posting or speaking in other server channels
	h.syncSpammerRoleChannelRestrictions(s, guildID, roleID, channelID)

	// Post or update the blatant warning message in the honeypot channel and pin it
	warnMsgID, err := h.ensureWarningMessage(s, channelID, h.svc.Config().WarningMessageID)
	if err != nil {
		h.logger.Warn("failed to post or update honeypot warning message", "channel_id", channelID, "error", err)
	}

	if err := h.svc.SetConfig(ctx, guildID, channelID, roleID, warnMsgID); err != nil {
		h.logger.Warn("failed to persist honeypot config to database", "error", err)
	}

	return channelID, roleID, warnMsgID, nil
}

func (h *HoneypotHandler) resolveOrCreateSpammerRole(s *discordgo.Session, guildID string) (string, error) {
	hpCfg := h.svc.Config()
	candidateID := strings.TrimSpace(hpCfg.SpammerRoleID)
	if candidateID == "" && h.cfg != nil {
		candidateID = strings.TrimSpace(h.cfg.SpammerRoleID)
	}

	roles, err := s.GuildRoles(guildID)
	if err == nil {
		if candidateID != "" {
			for _, r := range roles {
				if r != nil && r.ID == candidateID {
					return r.ID, nil
				}
			}
		}
		for _, r := range roles {
			if r != nil && strings.EqualFold(strings.TrimSpace(r.Name), domain.DefaultSpammerRoleName) {
				return r.ID, nil
			}
		}
	}

	color := 0xED4245 // Red
	hoist := false
	mentionable := false
	var perms int64 = 0

	created, createErr := s.GuildRoleCreate(guildID, &discordgo.RoleParams{
		Name:        domain.DefaultSpammerRoleName,
		Color:       &color,
		Hoist:       &hoist,
		Permissions: &perms,
		Mentionable: &mentionable,
	})
	if createErr != nil {
		return "", fmt.Errorf("creating %s role: %w", domain.DefaultSpammerRoleName, createErr)
	}

	if h.cache != nil {
		h.cache.SetRole(created.Name, created.ID)
	}
	h.logger.Info("created Spammer quarantine role", "guild_id", guildID, "role_id", created.ID)
	return created.ID, nil
}

func (h *HoneypotHandler) resolveOrCreateHoneypotChannel(
	s *discordgo.Session,
	guildID string,
	spammerRoleID string,
	explicitChannelID string,
) (string, error) {
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	overwrites := h.buildHoneypotOverwrites(guildID, botID, spammerRoleID)

	targetID := strings.TrimSpace(explicitChannelID)
	if targetID == "" {
		targetID = strings.TrimSpace(h.svc.Config().ChannelID)
	}
	if targetID == "" && h.cfg != nil {
		targetID = strings.TrimSpace(h.cfg.HoneypotChannelID)
	}

	if targetID != "" {
		if ch, err := s.Channel(targetID); err == nil && ch != nil {
			_, _ = s.ChannelEditComplex(ch.ID, &discordgo.ChannelEdit{
				Topic:                HoneypotChannelTopic,
				PermissionOverwrites: overwrites,
			})
			return ch.ID, nil
		}
	}

	if channels, err := s.GuildChannels(guildID); err == nil {
		for _, ch := range channels {
			if ch == nil || ch.Type != discordgo.ChannelTypeGuildText {
				continue
			}
			lower := strings.ToLower(strings.TrimSpace(ch.Name))
			if lower == domain.DefaultHoneypotChannelName ||
				strings.Contains(lower, "do-not-type-here") ||
				lower == "honeypot" ||
				lower == "⛔-honeypot" {
				_, _ = s.ChannelEditComplex(ch.ID, &discordgo.ChannelEdit{
					Topic:                HoneypotChannelTopic,
					PermissionOverwrites: overwrites,
				})
				return ch.ID, nil
			}
		}
	}

	created, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:                 domain.DefaultHoneypotChannelName,
		Type:                 discordgo.ChannelTypeGuildText,
		Topic:                HoneypotChannelTopic,
		PermissionOverwrites: overwrites,
	})
	if err != nil {
		return "", fmt.Errorf("creating honeypot channel: %w", err)
	}

	if h.cache != nil {
		h.cache.SetChannel(created.Name, created.ID)
	}
	h.logger.Info("created honeypot spam-trap channel", "guild_id", guildID, "channel_id", created.ID)
	return created.ID, nil
}

// buildHoneypotOverwrites configures the Honeypot channel permissions:
//   - @everyone, Unverified, and Verified roles can view and send plain text messages (so spam bots fall into the trap),
//     while attachments, embeds, threads, and reactions are denied to keep spam clean.
//   - Spammer role is explicitly denied ViewChannel and SendMessages so caught spammers cannot keep posting.
//   - Bot has full ManageMessages/SendMessages/EmbedLinks permissions.
func (h *HoneypotHandler) buildHoneypotOverwrites(guildID, botID, spammerRoleID string) []*discordgo.PermissionOverwrite {
	trapAllow := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionSendMessages)
	trapDeny := int64(
		discordgo.PermissionAttachFiles |
			discordgo.PermissionEmbedLinks |
			discordgo.PermissionAddReactions |
			discordgo.PermissionCreatePublicThreads |
			discordgo.PermissionCreatePrivateThreads |
			discordgo.PermissionSendMessagesInThreads |
			discordgo.PermissionMentionEveryone,
	)

	overwrites := []*discordgo.PermissionOverwrite{
		{
			ID:    guildID,
			Type:  discordgo.PermissionOverwriteTypeRole,
			Allow: trapAllow,
			Deny:  trapDeny,
		},
	}

	seen := map[string]bool{guildID: true}
	if h.cfg != nil {
		for _, rID := range []string{h.cfg.UnverifiedRoleID, h.cfg.VerifiedRoleID, h.cfg.IDVerifiedRoleID} {
			rID = strings.TrimSpace(rID)
			if rID != "" && !seen[rID] {
				seen[rID] = true
				overwrites = append(overwrites, &discordgo.PermissionOverwrite{
					ID:    rID,
					Type:  discordgo.PermissionOverwriteTypeRole,
					Allow: trapAllow,
					Deny:  trapDeny,
				})
			}
		}
	}

	if spammerRoleID != "" && !seen[spammerRoleID] {
		seen[spammerRoleID] = true
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:    spammerRoleID,
			Type:  discordgo.PermissionOverwriteTypeRole,
			Allow: 0,
			Deny: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionSendMessagesInThreads |
				discordgo.PermissionAddReactions,
		})
	}

	if botID != "" {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:   botID,
			Type: discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionReadMessageHistory |
				discordgo.PermissionEmbedLinks |
				discordgo.PermissionManageMessages |
				discordgo.PermissionManageChannels,
			Deny: 0,
		})
	}

	return overwrites
}

// syncSpammerRoleChannelRestrictions ensures the Spammer role is denied posting/speaking permissions
// across standard server channels while still allowing access to the Ticket Creation Hub so a false-positive
// human user can click a button to open an appeal ticket.
func (h *HoneypotHandler) syncSpammerRoleChannelRestrictions(s *discordgo.Session, guildID, spammerRoleID, honeypotChannelID string) {
	if s == nil || guildID == "" || spammerRoleID == "" {
		return
	}
	channels, err := s.GuildChannels(guildID)
	if err != nil {
		return
	}

	ticketCategoryID := ""
	if h.cfg != nil {
		ticketCategoryID = strings.TrimSpace(h.cfg.TicketCategoryID)
	}

	spammerDeny := int64(
		discordgo.PermissionSendMessages |
			discordgo.PermissionSendMessagesInThreads |
			discordgo.PermissionCreatePublicThreads |
			discordgo.PermissionCreatePrivateThreads |
			discordgo.PermissionAddReactions |
			discordgo.PermissionAttachFiles |
			discordgo.PermissionEmbedLinks |
			discordgo.PermissionVoiceConnect |
			discordgo.PermissionVoiceSpeak,
	)

	for _, ch := range channels {
		if ch == nil || ch.ID == honeypotChannelID {
			continue
		}
		// Do not restrict active private ticket channels inside the ticket category
		// (except the read-only ticket hub where buttons are used).
		if ticketCategoryID != "" && ch.ParentID == ticketCategoryID && ch.Name != DefaultTicketHubChannelName {
			continue
		}
		if ch.Type == discordgo.ChannelTypeGuildCategory || ch.Type == discordgo.ChannelTypeGuildText || ch.Type == discordgo.ChannelTypeGuildVoice {
			var allow int64 = 0
			deny := spammerDeny
			if ch.Name == DefaultTicketHubChannelName {
				// Allow quarantined users to view the Ticket Creation Hub so false-positives can open an Account Issue ticket
				allow = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory
			} else {
				deny |= discordgo.PermissionViewChannel
			}
			_ = s.ChannelPermissionSet(ch.ID, spammerRoleID, discordgo.PermissionOverwriteTypeRole, allow, deny)
		}
	}
}

func (h *HoneypotHandler) ensureWarningMessage(s *discordgo.Session, channelID, existingMsgID string) (string, error) {
	content := BuildHoneypotWarningContent()
	embed := BuildHoneypotWarningEmbed()

	if existingMsgID != "" {
		if msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
			ID:      existingMsgID,
			Channel: channelID,
			Content: &content,
			Embeds:  &[]*discordgo.MessageEmbed{embed},
		}); err == nil && msg != nil {
			_ = s.ChannelMessagePin(channelID, msg.ID)
			return msg.ID, nil
		}
	}

	// Check recent messages in the channel to see if the bot already posted the warning message
	if msgs, err := s.ChannelMessages(channelID, 20, "", "", ""); err == nil {
		for _, m := range msgs {
			if m != nil && m.Author != nil && m.Author.Bot && len(m.Embeds) > 0 && strings.Contains(m.Embeds[0].Title, "DO NOT TYPE IN THIS CHANNEL") {
				if edited, editErr := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
					ID:      m.ID,
					Channel: channelID,
					Content: &content,
					Embeds:  &[]*discordgo.MessageEmbed{embed},
				}); editErr == nil && edited != nil {
					_ = s.ChannelMessagePin(channelID, edited.ID)
					return edited.ID, nil
				}
			}
		}
	}

	sent, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: content,
		Embeds:  []*discordgo.MessageEmbed{embed},
	})
	if err != nil {
		return "", err
	}
	_ = s.ChannelMessagePin(channelID, sent.ID)
	return sent.ID, nil
}

// BuildHoneypotWarningContent returns the blatant plain-text header above the Honeypot warning embed.
func BuildHoneypotWarningContent() string {
	return "# ⛔ STOP! DO NOT TYPE IN THIS CHANNEL! ⛔\n" +
		"**HUMANS: DO NOT SEND ANY MESSAGE, IMAGE, OR EMOJI HERE!**\n" +
		"This channel is an **automated bot spam-trap (honeypot)**. Typing anything here will **immediately quarantine your account** with the **Spammer** role and **automatically kick you from the server after 3 days** unless Server Staff remove the quarantine."
}

// BuildHoneypotWarningEmbed constructs the blatant warning embed displayed inside the Honeypot channel.
func BuildHoneypotWarningEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: "🚨 ⛔ AUTOMATED SPAM-BOT HONEYPOT — DO NOT POST HERE! ⛔ 🚨",
		Description: "**Regular members must NEVER type in this channel.**\n\n" +
			"Compromised accounts and automated spam-bots blindly post links and messages into every writable channel they see. This channel exists **solely** to catch and isolate them automatically.",
		Color: 0xED4245, // Bright Crimson Red
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "🔒 What Happens If You Type Here?",
				Value: "1. Your message is **deleted immediately**.\n" +
					"2. All of your current server roles are **stripped**.\n" +
					"3. You are assigned the **Spammer** quarantine role, locking your access to the server.",
				Inline: false,
			},
			{
				Name: "⏳ 3-Day Automatic Kick Timer (72 Hours)",
				Value: "If the **Spammer** quarantine is **not removed by Server Staff within 3 days (72 hours)**, the bot will **automatically kick you from the server**.",
				Inline: false,
			},
			{
				Name: "🙋 Typed Here by Mistake?",
				Value: "If you are a real human who accidentally sent a message here, please **open an Account Issue / Support Ticket** or message a Server Staff member so they can verify you and run `/unquarantine` to restore your roles.",
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Automated Anti-Spam Honeypot • Do Not Type Here",
		},
	}
}

func (h *HoneypotHandler) sendQuarantineDM(s *discordgo.Session, userID, channelID string, q *domain.SpamQuarantine) {
	dmCh, err := s.UserChannelCreate(userID)
	if err != nil || dmCh == nil {
		return
	}

	embed := &discordgo.MessageEmbed{
		Title: "⛔ You Have Been Placed in Spammer Quarantine",
		Description: fmt.Sprintf(
			"You sent a message in <#%s>, which is clearly marked as our **Automated Bot Spam-Trap (Honeypot)** channel.\n\n"+
				"Your account has been assigned the **Spammer** quarantine role and your previous roles have been temporarily removed.",
			channelID,
		),
		Color: 0xED4245,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "⏳ Automatic Kick Deadline (3 Days)",
				Value:  fmt.Sprintf("If Server Staff do not remove your quarantine by **<t:%d:F>** (<t:%d:R>), you will be **automatically kicked** from the server.", q.KickAt.Unix(), q.KickAt.Unix()),
				Inline: false,
			},
			{
				Name:   "🙋 Are You a Human Who Made a Mistake?",
				Value:  "Please open an **Account Issue / Support Ticket** in the server or contact a **Server Staff** member so they can verify you and remove your quarantine.",
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Anti-Spam Quarantine System",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_, _ = s.ChannelMessageSendEmbed(dmCh.ID, embed)
}

func (h *HoneypotHandler) sendStaffQuarantineAlert(s *discordgo.Session, q *domain.SpamQuarantine, msgPreview string) {
	if h.cfg == nil {
		return
	}
	alertChID := strings.TrimSpace(h.cfg.StaffAlertChannelID)
	if alertChID == "" {
		alertChID = strings.TrimSpace(h.cfg.TicketLogChannelID)
	}
	if alertChID == "" {
		return
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🍯 Honeypot Triggered — Member Quarantined",
		Description: fmt.Sprintf("<@%s> (`%s`) posted in the Honeypot channel <#%s> and was automatically placed in **Spammer** quarantine.", q.UserID, q.Username, q.ChannelID),
		Color:       0xED4245,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "User",
				Value:  fmt.Sprintf("<@%s> (`%s`)", q.UserID, q.UserID),
				Inline: true,
			},
			{
				Name:   "Roles Stripped",
				Value:  fmt.Sprintf("`%d` role(s) saved for restoration", len(q.RemovedRoleIDs)),
				Inline: true,
			},
			{
				Name:   "3-Day Auto-Kick Deadline",
				Value:  fmt.Sprintf("<t:%d:F> (<t:%d:R>)", q.KickAt.Unix(), q.KickAt.Unix()),
				Inline: false,
			},
			{
				Name:   "Intercepted Message Content (Deleted)",
				Value:  msgPreview,
				Inline: false,
			},
			{
				Name:   "Staff Action",
				Value:  fmt.Sprintf("Click the button below or run `/unquarantine user:<@%s>` to release if this was a human false-positive.", q.UserID),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Honeypot Quarantine • 3-Day Auto-Kick Active",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					CustomID: HoneypotReleaseButtonPrefix + q.UserID,
					Label:    "Release from Quarantine",
					Style:    discordgo.SuccessButton,
					Emoji:    &discordgo.ComponentEmoji{Name: "🔓"},
				},
			},
		},
	}

	_, _ = s.ChannelMessageSendComplex(alertChID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})
}

func (h *HoneypotHandler) sendStaffReleaseNotice(s *discordgo.Session, q *domain.SpamQuarantine, targetUserID, actorID string) {
	if h.cfg == nil || s == nil {
		return
	}
	alertChID := strings.TrimSpace(h.cfg.StaffAlertChannelID)
	if alertChID == "" {
		alertChID = strings.TrimSpace(h.cfg.TicketLogChannelID)
	}
	if alertChID == "" {
		return
	}

	restored := 0
	if q != nil {
		restored = len(q.RemovedRoleIDs)
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🔓 Spammer Quarantine Released",
		Description: fmt.Sprintf("<@%s> was removed from Honeypot Spammer quarantine by <@%s>. Restored `%d` role(s) and cancelled the 3-day auto-kick timer.", targetUserID, actorID, restored),
		Color:       0x57F287,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	_, _ = s.ChannelMessageSendEmbed(alertChID, embed)
}

func (h *HoneypotHandler) handleQuarantineOutcome(q *domain.SpamQuarantine, outcome string) {
	if h.session == nil || h.cfg == nil || q == nil {
		return
	}
	alertChID := strings.TrimSpace(h.cfg.StaffAlertChannelID)
	if alertChID == "" {
		alertChID = strings.TrimSpace(h.cfg.TicketLogChannelID)
	}
	if alertChID == "" {
		return
	}

	switch outcome {
	case service.OutcomeKicked:
		embed := &discordgo.MessageEmbed{
			Title:       "🥾 Quarantined Spammer Auto-Kicked (3 Days Elapsed)",
			Description: fmt.Sprintf("<@%s> (`%s`) was **automatically kicked** from the server because their Honeypot **Spammer** quarantine was not removed within 3 days.", q.UserID, q.Username),
			Color:       0xED4245,
			Fields: []*discordgo.MessageEmbedField{
				{
					Name:   "Quarantined At",
					Value:  fmt.Sprintf("<t:%d:F>", q.QuarantinedAt.Unix()),
					Inline: true,
				},
				{
					Name:   "Kicked At",
					Value:  fmt.Sprintf("<t:%d:F>", time.Now().UTC().Unix()),
					Inline: true,
				},
			},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		_, _ = h.session.ChannelMessageSendEmbed(alertChID, embed)

	case service.OutcomeManualRoleDrop:
		embed := &discordgo.MessageEmbed{
			Title:       "🔓 Spammer Role Manually Removed",
			Description: fmt.Sprintf("The **Spammer** role was manually removed from <@%s> (`%s`), so their quarantine was marked released and their previous roles were restored.", q.UserID, q.Username),
			Color:       0x57F287,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		}
		_, _ = h.session.ChannelMessageSendEmbed(alertChID, embed)
	}
}

func (h *HoneypotHandler) isStaffMember(member *discordgo.Member) bool {
	if member == nil || h.cfg == nil {
		return false
	}
	if member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true
	}
	staffRoles := h.cfg.TicketStaffRoleIDs()
	staffSet := make(map[string]struct{}, len(staffRoles))
	for _, r := range staffRoles {
		staffSet[r] = struct{}{}
	}
	for _, r := range member.Roles {
		if _, ok := staffSet[r]; ok {
			return true
		}
	}
	return false
}
