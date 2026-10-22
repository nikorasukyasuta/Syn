package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/cache"
	"syn/internal/domain"
	"syn/internal/service"
)

var channelNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9\-]`)

// TicketHandler manages Discord slash commands, button interactions, and modal submissions
// for the ticket system.
type TicketHandler struct {
	svc      *service.TicketService
	cfg      *config.Config
	cache    *cache.ResourceCache
	logger   *slog.Logger
}

// NewTicketHandler constructs a new TicketHandler.
func NewTicketHandler(
	svc *service.TicketService,
	cfg *config.Config,
	resCache *cache.ResourceCache,
	logger *slog.Logger,
) *TicketHandler {
	return &TicketHandler{
		svc:    svc,
		cfg:    cfg,
		cache:  resCache,
		logger: logger,
	}
}

// TicketCommandDefinition builds the application command schema for /ticket.
func TicketCommandDefinition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "ticket",
		Description: "Support & verification ticket management",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "open",
				Description: "Open a new private ticket with server staff",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "type",
						Description: "The category of ticket you wish to open",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "🛡️ Verification Ticket", Value: "verification"},
							{Name: "🚨 Report Ticket", Value: "report"},
							{Name: "🐛 Bug Ticket", Value: "bug"},
							{Name: "👤 Account Issue Ticket", Value: "account_issue"},
							{Name: "💬 Support Ticket", Value: "support"},
						},
					},
					{
						Name:        "reason",
						Description: "Brief description of your issue or request",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    false,
					},
				},
			},
			{
				Name:        "close",
				Description: "Close the current ticket channel and record a resolution",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "resolution",
						Description: "Resolution status or notes",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    false,
					},
				},
			},
			{
				Name:        "panel",
				Description: "Post the interactive Ticket Creation Panel (Staff Only)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	}
}

// HandleTicketCommand routes incoming /ticket subcommands.
func (h *TicketHandler) HandleTicketCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		h.respondEphemeral(s, i, "Please specify a ticket subcommand: `/ticket open`, `/ticket close`, or `/ticket panel`.")
		return
	}

	subCmd := options[0]
	switch subCmd.Name {
	case "open":
		h.handleCommandOpen(s, i, subCmd.Options)
	case "close":
		h.handleCommandClose(s, i, subCmd.Options)
	case "panel":
		h.handleCommandPanel(s, i)
	default:
		h.respondEphemeral(s, i, "Unknown ticket subcommand.")
	}
}

// handleCommandOpen processes /ticket open type:<...> [reason:<...>].
func (h *TicketHandler) handleCommandOpen(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	optMap := optionsToMap(opts)
	rawType := ""
	if o, exists := optMap["type"]; exists {
		rawType = o.StringValue()
	}

	ticketType, err := domain.ParseTicketType(rawType)
	if err != nil {
		h.respondEphemeral(s, i, "Invalid ticket type specified.")
		return
	}

	reason := ""
	if o, exists := optMap["reason"]; exists {
		reason = o.StringValue()
	}

	h.processTicketCreation(s, i, ticketType, reason)
}

// handleCommandClose processes /ticket close [resolution:<...>].
func (h *TicketHandler) handleCommandClose(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	caller := i.Member.User
	callerRoles := i.Member.Roles

	optMap := optionsToMap(opts)
	if o, exists := optMap["resolution"]; exists && strings.TrimSpace(o.StringValue()) != "" {
		// Resolution was directly supplied via slash command argument
		resolution := o.StringValue()
		h.executeTicketClose(s, i, i.ChannelID, caller.ID, callerRoles, resolution)
		return
	}

	// Resolution not supplied: prompt the resolution modal
	h.promptCloseModal(s, i)
}

// handleCommandPanel sends the interactive ticket button panel (Staff only).
func (h *TicketHandler) handleCommandPanel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !h.svc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Insufficient permissions: `/ticket panel` is restricted to Server Staff and Helpers.")
		return
	}

	panelEmbed := BuildTicketPanelEmbed()
	panelRows := BuildTicketPanelButtons()

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{panelEmbed},
			Components: panelRows,
		},
	})
	if err != nil {
		h.logger.Error("failed to post ticket panel", "error", err)
	}
}

// HandleComponentInteraction processes button clicks:
//   - "btn_ticket_create:<type>"
//   - "btn_ticket_close"
func (h *TicketHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	if strings.HasPrefix(customID, "btn_ticket_create:") {
		rawType := strings.TrimPrefix(customID, "btn_ticket_create:")
		ticketType, err := domain.ParseTicketType(rawType)
		if err != nil {
			h.respondEphemeral(s, i, "Invalid ticket type requested.")
			return
		}
		h.processTicketCreation(s, i, ticketType, "")
		return
	}

	if customID == "btn_ticket_close" {
		h.promptCloseModal(s, i)
		return
	}
}

// HandleModalSubmit processes resolution modal submissions ("modal_ticket_close").
func (h *TicketHandler) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()
	if data.CustomID != "modal_ticket_close" {
		return
	}

	inputs := extractModalInputs(data.Components)
	status := strings.TrimSpace(inputs["resolution_status"])
	notes := strings.TrimSpace(inputs["resolution_notes"])

	if status == "" {
		status = "Resolved"
	}
	resolution := status
	if notes != "" {
		resolution = fmt.Sprintf("[%s] %s", status, notes)
	}

	caller := i.Member.User
	callerRoles := i.Member.Roles

	h.executeTicketClose(s, i, i.ChannelID, caller.ID, callerRoles, resolution)
}

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

	// 1. Enforce max 1 ticket of each type at a time
	existing, err := h.svc.CheckCanOpen(ctx, i.GuildID, caller.ID, ticketType)
	if errors.Is(err, domain.ErrTicketAlreadyOpen) {
		h.respondEphemeral(s, i, fmt.Sprintf(
			"❌ You already have an open %s in <#%s>. Please use or close that ticket before opening another.",
			ticketType.DisplayName(),
			existing.ChannelID,
		))
		return
	}
	if err != nil {
		h.logger.Error("error checking open tickets", "error", err, "user_id", caller.ID)
		h.respondEphemeral(s, i, "An error occurred while validating your active tickets. Please try again.")
		return
	}

	// 2. Defer interaction response to prevent Discord 3-second timeout during channel creation
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

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

// BuildTicketPanelEmbed generates the persistent ticket creation panel embed.
func BuildTicketPanelEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: "🎫 Server Support & Verification Tickets",
		Description: "Need assistance, want to report an issue, or request verification?\n" +
			"Click the button below that corresponds to your request to open a private ticket.\n\n" +
			"🛡️ **Verification Ticket** — Request 18+ verification or review\n" +
			"🚨 **Report Ticket** — Report server misconduct, harassment, or rule violations\n" +
			"🐛 **Bug Ticket** — Report bot glitches or server technical bugs\n" +
			"👤 **Account Issue Ticket** — Assistance with roles, access, or account issues\n" +
			"💬 **Support Ticket** — General server questions and support\n\n" +
			"*Note: You may only have one active ticket of each type at a time.*",
		Color: 0x5865F2,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Support Suite • Enterprise Security",
		},
	}
}

// BuildTicketPanelButtons generates the interactive action rows for the panel.
func BuildTicketPanelButtons() []discordgo.MessageComponent {
	row1 := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: "btn_ticket_create:verification",
				Label:    "Verification",
				Style:    discordgo.PrimaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "🛡️"},
			},
			discordgo.Button{
				CustomID: "btn_ticket_create:report",
				Label:    "Report",
				Style:    discordgo.DangerButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "🚨"},
			},
		},
	}

	row2 := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: "btn_ticket_create:bug",
				Label:    "Bug Report",
				Style:    discordgo.SecondaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "🐛"},
			},
			discordgo.Button{
				CustomID: "btn_ticket_create:account_issue",
				Label:    "Account Issue",
				Style:    discordgo.SecondaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "👤"},
			},
			discordgo.Button{
				CustomID: "btn_ticket_create:support",
				Label:    "Support",
				Style:    discordgo.SuccessButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "💬"},
			},
		},
	}

	return []discordgo.MessageComponent{row1, row2}
}

func (h *TicketHandler) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *TicketHandler) sendDeferredFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: message,
		Flags:   discordgo.MessageFlagsEphemeral,
	})
}

func extractModalInputs(components []discordgo.MessageComponent) map[string]string {
	inputs := make(map[string]string)
	for _, c := range components {
		var rowComponents []discordgo.MessageComponent
		switch row := c.(type) {
		case *discordgo.ActionsRow:
			rowComponents = row.Components
		case discordgo.ActionsRow:
			rowComponents = row.Components
		}
		for _, rc := range rowComponents {
			switch input := rc.(type) {
			case *discordgo.TextInput:
				inputs[input.CustomID] = input.Value
			case discordgo.TextInput:
				inputs[input.CustomID] = input.Value
			}
		}
	}
	return inputs
}

