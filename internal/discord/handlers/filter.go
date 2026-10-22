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
	"syn/internal/domain"
	"syn/internal/service"
)

// FilterHandler coordinates message deletion, ticket creation, staff reload commands,
// and warning actions for banned word violations.
type FilterHandler struct {
	filterSvc *service.WordFilterService
	ticketSvc *service.TicketService
	cfg       *config.Config
	cache     *cache.ResourceCache
	logger    *slog.Logger
}

// NewFilterHandler constructs a new FilterHandler.
func NewFilterHandler(
	filterSvc *service.WordFilterService,
	ticketSvc *service.TicketService,
	cfg *config.Config,
	resCache *cache.ResourceCache,
	logger *slog.Logger,
) *FilterHandler {
	return &FilterHandler{
		filterSvc: filterSvc,
		ticketSvc: ticketSvc,
		cfg:       cfg,
		cache:     resCache,
		logger:    logger,
	}
}

// FilterCommandDefinition returns the Discord slash command schema for /filter.
func FilterCommandDefinition() *discordgo.ApplicationCommand {
	var defaultPerms int64 = 0
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:                     "filter",
		Description:              "Manage word filters and reload cache (Staff Only)",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "reload",
				Description: "Reload word filter list from JSON configuration files into cache",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "channel",
						Description: "Specific channel whose filter file to reload (leave blank for global filter)",
						Type:        discordgo.ApplicationCommandOptionChannel,
						Required:    false,
					},
				},
			},
		},
	}
}

// WarningsCommandDefinition returns the Discord slash command schema for /warnings.
func WarningsCommandDefinition() *discordgo.ApplicationCommand {
	var defaultPerms int64 = 0
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:                     "warnings",
		Description:              "View moderation warning history for a user (Staff Only)",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "The member to view warning history for",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
		},
	}
}

// HandleMessageCreate inspects incoming messages for banned words, deleting violating messages
// and automatically provisioning a ticket for staff review.
func (h *FilterHandler) HandleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil || m.Author.Bot {
		return
	}

	// Filter only messages in the configured server
	if h.cfg.GuildID != "" && m.GuildID != "" && m.GuildID != h.cfg.GuildID {
		return
	}

	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}

	matched, match := h.filterSvc.CheckMessage(m.ChannelID, content)
	if !matched || match == nil {
		return
	}

	// 1. Immediately delete the violating message
	if err := s.ChannelMessageDelete(m.ChannelID, m.ID); err != nil {
		h.logger.Error("failed to delete filtered message",
			"channel_id", m.ChannelID,
			"message_id", m.ID,
			"author_id", m.Author.ID,
			"error", err,
		)
	} else {
		h.logger.Info("deleted message containing filtered word",
			"channel_id", m.ChannelID,
			"message_id", m.ID,
			"author_id", m.Author.ID,
			"matched_word", match.Word,
			"scope", match.Scope,
		)
	}

	// 2. Asynchronously provision ticket channel and notify staff
	go h.createFilterViolationTicket(s, m.GuildID, m.ChannelID, m.Author, content, match)
}

// createFilterViolationTicket opens a private ticket channel, adds the user and staff,
// posts the deleted content with action buttons, and persists the violation.
func (h *FilterHandler) createFilterViolationTicket(
	s *discordgo.Session,
	guildID, originChannelID string,
	author *discordgo.User,
	deletedContent string,
	match *domain.FilterMatch,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	categoryID := h.resolveTicketCategory(s, guildID)

	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	overwrites := h.buildChannelOverwrites(guildID, author.ID, botID)

	cleanName := channelNameSanitizer.ReplaceAllString(strings.ToLower(author.Username), "")
	if len(cleanName) > 12 {
		cleanName = cleanName[:12]
	}
	channelName := fmt.Sprintf("banned-word-%s", cleanName)

	createdChannel, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:                 channelName,
		Type:                 discordgo.ChannelTypeGuildText,
		ParentID:             categoryID,
		PermissionOverwrites: overwrites,
		Topic:                fmt.Sprintf("Filter violation by %s (%s)", author.Username, author.ID),
	})
	if err != nil {
		h.logger.Error("failed to create ticket channel for filter violation",
			"guild_id", guildID,
			"user_id", author.ID,
			"error", err,
		)
		return
	}

	// Record ticket in database
	ticket, err := h.ticketSvc.CreateTicket(ctx, guildID, createdChannel.ID, author.ID, domain.TicketTypeFilteredWords)
	if err != nil {
		h.logger.Error("failed to record filter violation ticket in database", "error", err, "channel_id", createdChannel.ID)
	}

	ticketID := ""
	if ticket != nil {
		ticketID = ticket.ID
	}

	// Persist violation details in database for staff button resolution
	violation := &domain.FilterViolation{
		GuildID:        guildID,
		ChannelID:      createdChannel.ID,
		TicketID:       ticketID,
		UserID:         author.ID,
		MessageContent: deletedContent,
		MatchedWord:    match.Word,
		OriginChannel:  originChannelID,
		CreatedAt:      time.Now().UTC(),
	}
	if err := h.filterSvc.SaveViolation(ctx, violation); err != nil {
		h.logger.Error("failed to save filter violation record", "error", err, "channel_id", createdChannel.ID)
	}

	// Post violation notice embed and action buttons inside ticket
	h.sendViolationEmbed(s, createdChannel.ID, author, originChannelID, deletedContent, match)
}

func (h *FilterHandler) sendViolationEmbed(
	s *discordgo.Session,
	ticketChannelID string,
	author *discordgo.User,
	originChannelID, deletedContent string,
	match *domain.FilterMatch,
) {
	scopeStr := "🌐 Server-Wide Global Filter"
	if match.Scope == domain.FilterScopeChannel {
		scopeStr = fmt.Sprintf("📍 Channel Specific Filter (<#%s>)", originChannelID)
	}

	displayContent := deletedContent
	if len(displayContent) > 900 {
		displayContent = displayContent[:900] + "... (truncated)"
	}

	embed := &discordgo.MessageEmbed{
		Title: "🚫 Banned Word Violation Detected",
		Description: fmt.Sprintf(
			"A message sent by <@%s> triggered the automated word filter and was immediately deleted.\n\n"+
				"Staff members must review the incident below and choose to either issue a **formal warning** "+
				"or **close without warning**.",
			author.ID,
		),
		Color: 0xE74C3C, // Red
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Offending User",
				Value:  fmt.Sprintf("<@%s> (`%s` / ID: `%s`)", author.ID, author.Username, author.ID),
				Inline: true,
			},
			{
				Name:   "Origin Channel",
				Value:  fmt.Sprintf("<#%s>", originChannelID),
				Inline: true,
			},
			{
				Name:   "Filter Scope",
				Value:  scopeStr,
				Inline: true,
			},
			{
				Name:   "Filtered Word Triggered",
				Value:  fmt.Sprintf("||`%s`||", match.Word),
				Inline: false,
			},
			{
				Name:   "Deleted Message Content",
				Value:  fmt.Sprintf("```\n%s\n```", displayContent),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Security Suite • Automated Filter Violation",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	actionButtons := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: "btn_filter_warn",
				Label:    "Warn User",
				Style:    discordgo.DangerButton,
				Emoji: &discordgo.ComponentEmoji{
					Name: "⚠️",
				},
			},
			discordgo.Button{
				CustomID: "btn_filter_close_no_warn",
				Label:    "Close without Warning",
				Style:    discordgo.SecondaryButton,
				Emoji: &discordgo.ComponentEmoji{
					Name: "✖️",
				},
			},
		},
	}

	var staffPings strings.Builder
	for _, roleID := range h.cfg.StaffRoleIDs {
		staffPings.WriteString(fmt.Sprintf("<@&%s> ", roleID))
	}

	content := fmt.Sprintf("<@%s> %s", author.ID, strings.TrimSpace(staffPings.String()))

	_, err := s.ChannelMessageSendComplex(ticketChannelID, &discordgo.MessageSend{
		Content:    content,
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{actionButtons},
	})
	if err != nil {
		h.logger.Error("failed to post violation embed to ticket channel",
			"channel_id", ticketChannelID,
			"error", err,
		)
	}
}

// HandleFilterCommand processes the /filter reload slash command.
func (h *FilterHandler) HandleFilterCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || !h.filterSvc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Insufficient permissions: `/filter` is restricted to server staff.")
		return
	}

	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 || opts[0].Name != "reload" {
		h.respondEphemeral(s, i, "Please specify `/filter reload [channel]`.")
		return
	}

	subOpts := opts[0].Options
	var targetChannelID string
	for _, opt := range subOpts {
		if opt.Name == "channel" && opt.ChannelValue(s) != nil {
			targetChannelID = opt.ChannelValue(s).ID
		}
	}

	filtersDir := h.cfg.FiltersDir
	if filtersDir == "" {
		filtersDir = "./config/filters"
	}

	if targetChannelID != "" {
		// Specific channel reload: only update cache if file is found!
		found, count, err := h.filterSvc.ReloadChannel(filtersDir, targetChannelID)
		if err != nil {
			h.respondEphemeral(s, i, fmt.Sprintf("❌ Error parsing filter file for <#%s>: %v", targetChannelID, err))
			return
		}
		if !found {
			h.respondEphemeral(s, i, fmt.Sprintf("⚠️ Filter file for <#%s> was not found in `%s`. Existing in-memory cache was not modified.", targetChannelID, filtersDir))
			return
		}
		h.respondEphemeral(s, i, fmt.Sprintf("✅ Successfully reloaded filter cache for <#%s> (%d word(s) cached).", targetChannelID, count))
		return
	}

	// Global filter reload: only update cache if file is found!
	found, count, err := h.filterSvc.ReloadGlobal(filtersDir)
	if err != nil {
		h.respondEphemeral(s, i, fmt.Sprintf("❌ Error parsing global filter file: %v", err))
		return
	}
	if !found {
		h.respondEphemeral(s, i, fmt.Sprintf("⚠️ Global filter file `global.json` was not found in `%s`. Existing in-memory cache was not modified.", filtersDir))
		return
	}
	h.respondEphemeral(s, i, fmt.Sprintf("✅ Successfully reloaded global server filter cache (%d word(s) cached from `global.json`).", count))
}

// HandleComponentInteraction processes button clicks on banned words tickets:
//   - "btn_filter_warn"
//   - "btn_filter_close_no_warn"
func (h *FilterHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	customID := i.MessageComponentData().CustomID

	switch customID {
	case "btn_filter_warn":
		h.handleWarnButton(s, i)
		return true
	case "btn_filter_close_no_warn":
		h.handleCloseNoWarnButton(s, i)
		return true
	default:
		return false
	}
}

func (h *FilterHandler) handleWarnButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || !h.filterSvc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Access Denied: Only server staff can issue warnings.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminID := i.Member.User.ID
	channelID := i.ChannelID
	guildID := i.GuildID

	// 1. Fetch violation record from database
	violation, err := h.filterSvc.GetViolation(ctx, guildID, channelID)
	if err != nil {
		h.logger.Error("error retrieving violation record for warning", "channel_id", channelID, "error", err)
	}

	targetUserID := ""
	matchedRule := "banned word"
	deletedContent := "(message content unavailable)"
	originChannelID := ""
	ticketID := ""

	if violation != nil {
		targetUserID = violation.UserID
		matchedRule = violation.MatchedWord
		deletedContent = violation.MessageContent
		originChannelID = violation.OriginChannel
		ticketID = violation.TicketID
	}

	// Fallback to ticket record if violation not found
	if targetUserID == "" {
		if ticket, tErr := h.ticketSvc.GetTicketByChannel(ctx, guildID, channelID); tErr == nil && ticket != nil {
			targetUserID = ticket.CreatorID
			ticketID = ticket.ID
		}
	}

	if targetUserID == "" {
		h.respondEphemeral(s, i, "❌ Could not resolve the offending user for this ticket.")
		return
	}

	// 2. Persist formal warning record in PostgreSQL
	warning := &domain.UserWarning{
		GuildID:        guildID,
		UserID:         targetUserID,
		AdminID:        adminID,
		MessageContent: deletedContent,
		RuleMatched:    matchedRule,
		ChannelID:      originChannelID,
		TicketID:       ticketID,
		Reason:         fmt.Sprintf("Filtered word violation: %s", matchedRule),
		CreatedAt:      time.Now().UTC(),
	}

	if err := h.filterSvc.RecordWarning(ctx, warning); err != nil {
		h.logger.Error("failed to persist user warning", "error", err, "user_id", targetUserID)
		h.respondEphemeral(s, i, "❌ Database error while recording warning. Please check logs.")
		return
	}

	h.logger.Info("user warning recorded successfully",
		"user_id", targetUserID,
		"admin_id", adminID,
		"rule_matched", matchedRule,
		"channel_id", channelID,
	)

	// 3. Close the ticket in database
	resolution := fmt.Sprintf("Warned by <@%s> for filtered word: %s", adminID, matchedRule)
	closedTicket, cErr := h.ticketSvc.CloseTicket(ctx, guildID, channelID, adminID, i.Member.Roles, resolution)
	if cErr != nil && !errors.Is(cErr, domain.ErrTicketAlreadyClosed) {
		h.logger.Warn("error marking filter ticket closed", "error", cErr, "channel_id", channelID)
	}

	// 4. Send confirmation embed into the ticket channel
	warnEmbed := &discordgo.MessageEmbed{
		Title: "⚠️ Official Warning Issued",
		Description: fmt.Sprintf(
			"<@%s> has been issued an official moderation warning by <@%s>.\n\n"+
				"**Violation:** Filtered word `||%s||`\n"+
				"**Recorded Message Snippet:**\n> %s\n\n"+
				"*A permanent record of this warning has been added to the database.\n"+
				"This channel will be archived and deleted in 5 seconds.*",
			targetUserID, adminID, matchedRule, deletedContent,
		),
		Color:     0xE74C3C, // Red
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{warnEmbed},
		},
	})

	// 5. Log resolution to audit channel
	if closedTicket != nil {
		h.logTicketClosure(s, closedTicket, adminID)
	}

	// 6. Delete ticket channel after 5 seconds
	time.AfterFunc(5*time.Second, func() {
		if _, err := s.ChannelDelete(channelID); err != nil {
			h.logger.Warn("failed to delete ticket channel after warning", "channel_id", channelID, "error", err)
		}
	})
}

func (h *FilterHandler) handleCloseNoWarnButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || !h.filterSvc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Access Denied: Only server staff can close tickets.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminID := i.Member.User.ID
	channelID := i.ChannelID
	guildID := i.GuildID

	// Close the ticket in database without recording a warning
	resolution := fmt.Sprintf("Dismissed / Closed without warning by <@%s>", adminID)
	closedTicket, cErr := h.ticketSvc.CloseTicket(ctx, guildID, channelID, adminID, i.Member.Roles, resolution)
	if cErr != nil && !errors.Is(cErr, domain.ErrTicketAlreadyClosed) {
		h.logger.Warn("error marking filter ticket closed", "error", cErr, "channel_id", channelID)
	}

	closeEmbed := &discordgo.MessageEmbed{
		Title: "✖️ Ticket Closed Without Warning",
		Description: fmt.Sprintf(
			"This incident was reviewed and dismissed by <@%s>. No moderation warning was recorded.\n\n"+
				"*This channel will be archived and deleted in 5 seconds.*",
			adminID,
		),
		Color:     0x95A5A6, // Gray
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{closeEmbed},
		},
	})

	// Log closure to audit channel
	if closedTicket != nil {
		h.logTicketClosure(s, closedTicket, adminID)
	}

	// Delete channel after 5 seconds
	time.AfterFunc(5*time.Second, func() {
		if _, err := s.ChannelDelete(channelID); err != nil {
			h.logger.Warn("failed to delete ticket channel after dismissal", "channel_id", channelID, "error", err)
		}
	})
}

// HandleWarningsCommand processes /warnings user:<@user> (Staff only).
func (h *FilterHandler) HandleWarningsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || !h.filterSvc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Access Denied: `/warnings` is restricted to server staff.")
		return
	}

	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		h.respondEphemeral(s, i, "Target user is required.")
		return
	}

	targetUser := opts[0].UserValue(s)
	if targetUser == nil {
		h.respondEphemeral(s, i, "Could not resolve target member.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	warnings, err := h.filterSvc.GetWarnings(ctx, i.GuildID, targetUser.ID)
	if err != nil {
		h.respondEphemeral(s, i, fmt.Sprintf("❌ Error querying warning records: %v", err))
		return
	}

	if len(warnings) == 0 {
		h.respondEphemeral(s, i, fmt.Sprintf("🟢 <@%s> has **0** recorded moderation warnings.", targetUser.ID))
		return
	}

	var fields []*discordgo.MessageEmbedField
	for idx, w := range warnings {
		if idx >= 10 {
			fields = append(fields, &discordgo.MessageEmbedField{
				Name:   "Additional Records",
				Value:  fmt.Sprintf("*... and %d more warning(s)*", len(warnings)-10),
				Inline: false,
			})
			break
		}
		msgSnippet := w.MessageContent
		if len(msgSnippet) > 100 {
			msgSnippet = msgSnippet[:100] + "..."
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: fmt.Sprintf("Warning #%d • <t:%d:d>", len(warnings)-idx, w.CreatedAt.Unix()),
			Value: fmt.Sprintf("Admin: <@%s>\nRule: `||%s||`\nMessage: > %s",
				w.AdminID, w.RuleMatched, msgSnippet),
			Inline: false,
		})
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("⚠️ Moderation History • %s", targetUser.Username),
		Description: fmt.Sprintf("Found **%d** formal warning(s) on file for <@%s>.", len(warnings), targetUser.ID),
		Color:       0xF39C12, // Orange
		Fields:      fields,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("User ID: %s", targetUser.ID),
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

func (h *FilterHandler) buildChannelOverwrites(guildID, creatorID, botID string) []*discordgo.PermissionOverwrite {
	var overwrites []*discordgo.PermissionOverwrite

	// 1. Deny @everyone ViewChannel
	overwrites = append(overwrites, &discordgo.PermissionOverwrite{
		ID:    guildID,
		Type:  discordgo.PermissionOverwriteTypeRole,
		Deny:  discordgo.PermissionViewChannel,
		Allow: 0,
	})

	// 2. Allow Bot full channel access
	if botID != "" {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:   botID,
			Type: discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionManageChannels |
				discordgo.PermissionEmbedLinks |
				discordgo.PermissionAttachFiles |
				discordgo.PermissionReadMessageHistory,
			Deny: 0,
		})
	}

	// 3. Allow Ticket Creator (violator) ViewChannel + ReadMessageHistory
	if creatorID != "" {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:   creatorID,
			Type: discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionReadMessageHistory |
				discordgo.PermissionAttachFiles,
			Deny: 0,
		})
	}

	// 4. Allow Staff and Server Helper roles full access
	for _, roleID := range h.cfg.StaffRoleIDs {
		if roleID == "" {
			continue
		}
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:   roleID,
			Type: discordgo.PermissionOverwriteTypeRole,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionReadMessageHistory |
				discordgo.PermissionEmbedLinks |
				discordgo.PermissionAttachFiles |
				discordgo.PermissionManageMessages,
			Deny: 0,
		})
	}

	return overwrites
}

func (h *FilterHandler) resolveTicketCategory(s *discordgo.Session, guildID string) string {
	if h.cfg.TicketCategoryID != "" {
		return h.cfg.TicketCategoryID
	}
	if h.cache != nil {
		if id, exists := h.cache.GetChannelID("ticket"); exists {
			return id
		}
		if id, exists := h.cache.GetChannelID("tickets"); exists {
			return id
		}
	}
	channels, err := s.GuildChannels(guildID)
	if err == nil {
		for _, ch := range channels {
			if ch.Type == discordgo.ChannelTypeGuildCategory {
				lowerName := strings.ToLower(ch.Name)
				if lowerName == "ticket" || lowerName == "tickets" || strings.Contains(lowerName, "ticket") {
					return ch.ID
				}
			}
		}
	}
	return ""
}

func (h *FilterHandler) logTicketClosure(s *discordgo.Session, t *domain.Ticket, closedByID string) {
	logChannelID := h.cfg.TicketLogChannelID
	if logChannelID == "" {
		return
	}

	duration := "N/A"
	if t.ClosedAt != nil {
		duration = t.ClosedAt.Sub(t.CreatedAt).Round(time.Second).String()
	}

	logEmbed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("📋 Filter Violation Ticket Closed: %s", t.Type.DisplayName()),
		Description: fmt.Sprintf("Ticket channel `<#%s>` (`%s`) was resolved.", t.ChannelID, t.ChannelID),
		Color:       0xE67E22,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Ticket Type",
				Value:  fmt.Sprintf("%s %s", t.Type.Emoji(), t.Type.DisplayName()),
				Inline: true,
			},
			{
				Name:   "Offending User",
				Value:  fmt.Sprintf("<@%s>", t.CreatorID),
				Inline: true,
			},
			{
				Name:   "Closed By",
				Value:  fmt.Sprintf("<@%s>", closedByID),
				Inline: true,
			},
			{
				Name:   "Resolution",
				Value:  t.Resolution,
				Inline: false,
			},
			{
				Name:   "Open Duration",
				Value:  duration,
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Ticket ID: %s", t.ID),
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_, _ = s.ChannelMessageSendEmbed(logChannelID, logEmbed)
}

func (h *FilterHandler) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		})
	}
}
