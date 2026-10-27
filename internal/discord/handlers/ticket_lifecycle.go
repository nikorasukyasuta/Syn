package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/domain"
)

// processTicketCreation creates the ticket channel, permissions, initial embed, and DB record.
func (h *TicketHandler) processTicketCreation(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	ticketType domain.TicketType,
	initialReason string,
) {
	caller := i.Member.User
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Immediately defer interaction response to prevent Discord 3-second timeout
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	// 2. Enforce max 1 ticket of each type at a time
	existing, err := h.svc.CheckCanOpen(ctx, i.GuildID, caller.ID, ticketType)
	if errors.Is(err, domain.ErrTicketAlreadyOpen) {
		h.sendDeferredFollowup(s, i, fmt.Sprintf(
			"❌ You already have an open %s in <#%s>. Please use or close that ticket before opening another.",
			ticketType.DisplayName(),
			existing.ChannelID,
		))
		return
	}
	if err != nil {
		h.logger.Error("error checking open tickets", "error", err, "user_id", caller.ID)
		h.sendDeferredFollowup(s, i, "An error occurred while validating your active tickets. Please try again.")
		return
	}

	// 3. Resolve parent category
	categoryID := h.resolveTicketCategory(s, i.GuildID)

	// 4. Construct permission overwrites: Staff + Ticket Creator ONLY
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	overwrites := h.buildChannelOverwrites(i.GuildID, caller.ID, botID)

	// 5. Generate sanitized channel name
	cleanName := channelNameSanitizer.ReplaceAllString(strings.ToLower(caller.Username), "")
	if len(cleanName) > 15 {
		cleanName = cleanName[:15]
	}
	channelName := fmt.Sprintf("%s-%s", ticketType.ChannelPrefix(), cleanName)

	// 6. Create channel via Discord API
	createdChannel, err := s.GuildChannelCreateComplex(i.GuildID, discordgo.GuildChannelCreateData{
		Name:                 channelName,
		Type:                 discordgo.ChannelTypeGuildText,
		ParentID:             categoryID,
		PermissionOverwrites: overwrites,
		Topic:                fmt.Sprintf("%s for %s (%s)", ticketType.DisplayName(), caller.Username, caller.ID),
	})
	if err != nil {
		h.logger.Error("failed to create discord ticket channel", "error", err, "guild_id", i.GuildID, "creator_id", caller.ID)
		h.sendDeferredFollowup(s, i, "❌ Failed to create ticket channel. Please verify bot permissions ('Manage Channels').")
		return
	}

	// 7. Persist ticket in database
	ticket, err := h.svc.CreateTicket(ctx, i.GuildID, createdChannel.ID, caller.ID, ticketType)
	if err != nil {
		h.logger.Error("failed to record ticket in database", "error", err, "channel_id", createdChannel.ID)
		// Clean up orphaned channel
		_, _ = s.ChannelDelete(createdChannel.ID)
		h.sendDeferredFollowup(s, i, "❌ Database error creating ticket record. Please contact an administrator.")
		return
	}

	// 8. Post welcome embed and Close Ticket button inside the newly created channel
	h.sendTicketWelcomeMessage(s, createdChannel.ID, caller.ID, ticket, initialReason)

	// 9. Respond ephemerally to the creator with the direct link to the channel
	h.sendDeferredFollowup(s, i, fmt.Sprintf("✅ Your %s has been created: <#%s>", ticketType.DisplayName(), createdChannel.ID))
}

// promptCloseModal opens the resolution input modal for closing a ticket.
func (h *TicketHandler) promptCloseModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	caller := i.Member.User
	callerRoles := i.Member.Roles

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ticket, err := h.svc.GetTicketByChannel(ctx, i.GuildID, i.ChannelID)
	if err != nil {
		h.respondEphemeral(s, i, "❌ This channel is not an active ticket channel.")
		return
	}

	if !h.svc.CanCloseTicket(caller.ID, callerRoles, ticket) {
		h.respondEphemeral(s, i, "❌ Only the ticket creator or server staff can close this ticket.")
		return
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "modal_ticket_close",
			Title:    "Close Ticket - Resolution",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "resolution_status",
							Label:       "Resolution Status",
							Style:       discordgo.TextInputShort,
							Placeholder: "e.g. Resolved, Answered, Duplicate, Canceled",
							Value:       "Resolved",
							Required:    true,
							MaxLength:   64,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "resolution_notes",
							Label:       "Resolution Notes / Details",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "Optional explanation or resolution summary...",
							Required:    false,
							MaxLength:   1000,
						},
					},
				},
			},
		},
	})
	if err != nil {
		h.logger.Error("failed to display ticket resolution modal", "error", err)
	}
}

// executeTicketClose performs the close operation in DB, sends confirmation, logs, and deletes channel.
func (h *TicketHandler) executeTicketClose(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	channelID, callerID string,
	callerRoles []string,
	resolution string,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	closedTicket, err := h.svc.CloseTicket(ctx, i.GuildID, channelID, callerID, callerRoles, resolution)
	if err != nil {
		if errors.Is(err, domain.ErrUnauthorizedToClose) {
			h.respondEphemeral(s, i, "❌ Only the ticket creator or server staff can close this ticket.")
			return
		}
		if errors.Is(err, domain.ErrTicketAlreadyClosed) {
			h.respondEphemeral(s, i, "⚠️ This ticket has already been marked as closed.")
			return
		}
		h.logger.Error("error closing ticket", "error", err, "channel_id", channelID)
		h.respondEphemeral(s, i, "❌ An error occurred while closing the ticket.")
		return
	}

	// 1. Send closing message in the ticket channel
	closeEmbed := &discordgo.MessageEmbed{
		Title:       "🔒 Ticket Closed",
		Description: fmt.Sprintf("This ticket was closed by <@%s>.\n\n**Resolution:** %s\n\n*This channel will be archived and deleted in 5 seconds.*", callerID, closedTicket.Resolution),
		Color:       0xE74C3C, // Red
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	if i.Type == discordgo.InteractionModalSubmit {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{closeEmbed},
			},
		})
	} else {
		_, _ = s.ChannelMessageSendEmbed(channelID, closeEmbed)
		h.respondEphemeral(s, i, "✅ Ticket closed successfully.")
	}

	// 2. Log resolution to audit / ticket log channel
	h.logTicketClosure(s, closedTicket, callerID)

	// 3. Schedule channel deletion after 5 seconds
	time.AfterFunc(5*time.Second, func() {
		if _, err := s.ChannelDelete(channelID); err != nil {
			h.logger.Warn("failed to auto-delete closed ticket channel", "channel_id", channelID, "error", err)
		}
	})
}

// logTicketClosure dispatches an audit embed to the configured or discovered ticket log channel.
func (h *TicketHandler) logTicketClosure(s *discordgo.Session, t *domain.Ticket, closedByID string) {
	logChannelID := h.resolveLogChannel(s, t.GuildID)
	if logChannelID == "" {
		h.logger.Debug("no ticket log channel configured or found, skipping channel log dispatch")
		return
	}

	duration := "N/A"
	if t.ClosedAt != nil {
		duration = t.ClosedAt.Sub(t.CreatedAt).Round(time.Second).String()
	}

	logEmbed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("📋 Ticket Closed: %s", t.Type.DisplayName()),
		Description: fmt.Sprintf("Ticket channel `<#%s>` (`%s`) was resolved and closed.", t.ChannelID, t.ChannelID),
		Color:       0xE67E22, // Orange
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Ticket Type",
				Value:  fmt.Sprintf("%s %s", t.Type.Emoji(), t.Type.DisplayName()),
				Inline: true,
			},
			{
				Name:   "Opened By",
				Value:  fmt.Sprintf("<@%s> (`%s`)", t.CreatorID, t.CreatorID),
				Inline: true,
			},
			{
				Name:   "Closed By",
				Value:  fmt.Sprintf("<@%s> (`%s`)", closedByID, closedByID),
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
			{
				Name:   "Opened At",
				Value:  fmt.Sprintf("<t:%d:F>", t.CreatedAt.Unix()),
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Ticket ID: %s", t.ID),
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	if _, err := s.ChannelMessageSendEmbed(logChannelID, logEmbed); err != nil {
		h.logger.Warn("failed to send ticket closure embed to log channel", "log_channel_id", logChannelID, "error", err)
	}
}

// resolveTicketCategory checks configured category ID or searches for a "ticket" category.
func (h *TicketHandler) resolveTicketCategory(s *discordgo.Session, guildID string) string {
	if h.cfg.TicketCategoryID != "" {
		return h.cfg.TicketCategoryID
	}

	// Look up in dynamic resource cache
	if h.cache != nil {
		if id, exists := h.cache.GetChannelID("ticket"); exists {
			return id
		}
		if id, exists := h.cache.GetChannelID("tickets"); exists {
			return id
		}
	}

	// Query guild channels directly
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

// resolveLogChannel finds the log channel via config or channel search.
func (h *TicketHandler) resolveLogChannel(s *discordgo.Session, guildID string) string {
	if h.cfg.TicketLogChannelID != "" {
		return h.cfg.TicketLogChannelID
	}

	// Check resource cache
	if h.cache != nil {
		if id, exists := h.cache.GetChannelID("ticket-logs"); exists {
			return id
		}
		if id, exists := h.cache.GetChannelID("ticket-log"); exists {
			return id
		}
	}

	// Search guild channels
	channels, err := s.GuildChannels(guildID)
	if err == nil {
		for _, ch := range channels {
			if ch.Type == discordgo.ChannelTypeGuildText {
				lowerName := strings.ToLower(ch.Name)
				if lowerName == "ticket-logs" || lowerName == "ticket-log" || lowerName == "tickets-log" {
					return ch.ID
				}
			}
		}
	}

	return ""
}

// buildChannelOverwrites creates strict permission overwrites: Staff + Creator ONLY.
func (h *TicketHandler) buildChannelOverwrites(guildID, creatorID, botID string) []*discordgo.PermissionOverwrite {
	overwrites := []*discordgo.PermissionOverwrite{
		// 1. Hide from @everyone
		{
			ID:    guildID,
			Type:  discordgo.PermissionOverwriteTypeRole,
			Deny:  discordgo.PermissionViewChannel,
			Allow: 0,
		},
		// 2. Allow ticket creator
		{
			ID:   creatorID,
			Type: discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionReadMessageHistory |
				discordgo.PermissionAttachFiles |
				discordgo.PermissionEmbedLinks,
			Deny: 0,
		},
	}

	// 3. Allow Staff roles
	for _, staffRoleID := range h.cfg.StaffRoleIDs {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:   staffRoleID,
			Type: discordgo.PermissionOverwriteTypeRole,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionReadMessageHistory |
				discordgo.PermissionAttachFiles |
				discordgo.PermissionEmbedLinks |
				discordgo.PermissionManageMessages,
			Deny: 0,
		})
	}

	// 4. Allow bot itself
	if botID != "" {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:   botID,
			Type: discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionReadMessageHistory |
				discordgo.PermissionAttachFiles |
				discordgo.PermissionEmbedLinks |
				discordgo.PermissionManageChannels |
				discordgo.PermissionManageMessages,
			Deny: 0,
		})
	}

	return overwrites
}

// sendTicketWelcomeMessage formats the initial ticket management embed with the Close button.
func (h *TicketHandler) sendTicketWelcomeMessage(
	s *discordgo.Session,
	channelID, creatorID string,
	ticket *domain.Ticket,
	initialReason string,
) {
	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("%s %s", ticket.Type.Emoji(), ticket.Type.DisplayName()),
		Description: fmt.Sprintf(
			"Welcome <@%s>!\n\nOur server staff team has been notified and will assist you shortly. "+
				"Please describe your request, issue, or evidence in detail below.",
			creatorID,
		),
		Color: 0x5865F2,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Ticket Type",
				Value:  fmt.Sprintf("%s %s", ticket.Type.Emoji(), ticket.Type.DisplayName()),
				Inline: true,
			},
			{
				Name:   "Opened By",
				Value:  fmt.Sprintf("<@%s>", creatorID),
				Inline: true,
			},
			{
				Name:   "Status",
				Value:  "🟢 Open",
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Click the Close Ticket button below when finished.",
		},
		Timestamp: ticket.CreatedAt.Format(time.RFC3339),
	}

	if initialReason != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Initial Notes",
			Value:  initialReason,
			Inline: false,
		})
	}

	closeButton := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: "btn_ticket_close",
				Label:    "Close Ticket",
				Style:    discordgo.DangerButton,
				Emoji: &discordgo.ComponentEmoji{
					Name: "🔒",
				},
			},
		},
	}

	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:    fmt.Sprintf("<@%s>", creatorID),
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{closeButton},
	})
	if err != nil {
		h.logger.Error("failed to post welcome message to ticket channel", "channel_id", channelID, "error", err)
	}
}

