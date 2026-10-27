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
				Description: "Provision or sync the read-only Ticket Creation Hub channel and all flow posts (Staff Only)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "update-post",
				Description: "Dynamically update a specific ticket type's post and embed in-place (Staff Only)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "type",
						Description: "Which ticket flow post in the Ticket Hub channel to update",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "🪪 18+ ID Verification", Value: string(domain.PanelTypeIDVerification)},
							{Name: "💬 Support Ticket", Value: string(domain.PanelTypeSupport)},
							{Name: "👤 Account Issue Ticket", Value: string(domain.PanelTypeAccountIssue)},
							{Name: "🚨 Report Ticket", Value: string(domain.PanelTypeReport)},
							{Name: "🐛 Bug Ticket", Value: string(domain.PanelTypeBug)},
						},
					},
					{
						Name:        "title",
						Description: "New embed title (leave blank to open interactive editor modal)",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    false,
					},
					{
						Name:        "description",
						Description: "New embed description / instructions (leave blank to open interactive editor modal)",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    false,
					},
					{
						Name:        "button_label",
						Description: "New button text (leave blank to open interactive editor modal)",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    false,
					},
				},
			},
		},
	}
}

// HandleTicketCommand routes incoming /ticket subcommands.
func (h *TicketHandler) HandleTicketCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		h.respondEphemeral(s, i, "Please specify a ticket subcommand: `/ticket open`, `/ticket close`, `/ticket panel`, or `/ticket update-post`.")
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
	case "update-post":
		h.handleCommandUpdatePost(s, i, subCmd.Options)
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

// handleCommandPanel provisions or synchronizes the read-only Ticket Creation Hub channel
// in the ticket category with individual, in-place updatable posts for each ticket & ID verification flow (Staff only).
func (h *TicketHandler) handleCommandPanel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || !h.svc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Insufficient permissions: `/ticket panel` is restricted to Server Staff and Helpers.")
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	hubChannelID, err := h.EnsureTicketHub(ctx, s, guildID)
	if err != nil {
		h.logger.Error("failed to provision or sync ticket creation hub", "error", err, "guild_id", guildID)
		h.sendDeferredFollowup(s, i, fmt.Sprintf("❌ Failed to synchronize Ticket Creation Hub: %v", err))
		return
	}

	h.sendDeferredFollowup(s, i, fmt.Sprintf(
		"✅ **Ticket Creation Hub Synchronized!**\nChannel: <#%s>\nAll 5 individual ticket & 18+ ID verification flow posts have been synced in-place (read-only for members, interactive buttons only).",
		hubChannelID,
	))
}

// handleCommandUpdatePost allows Staff to dynamically update a single ticket type's embed and button
// in-place without changing its position in the Ticket Creation Hub channel.
func (h *TicketHandler) handleCommandUpdatePost(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	if i.Member == nil || !h.svc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Insufficient permissions: `/ticket update-post` is restricted to Server Staff and Helpers.")
		return
	}

	optMap := optionsToMap(opts)
	rawType := ""
	if o, exists := optMap["type"]; exists {
		rawType = o.StringValue()
	}

	panelType, err := domain.ParseTicketPanelType(rawType)
	if err != nil {
		h.respondEphemeral(s, i, "❌ Invalid ticket post type specified.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	post, err := h.svc.GetOrInitPanelPost(ctx, guildID, panelType)
	if err != nil || post == nil {
		h.respondEphemeral(s, i, "❌ Failed to load ticket post configuration.")
		return
	}

	var newTitle, newDesc, newBtnLabel string
	hasInlineEdits := false

	if o, exists := optMap["title"]; exists && strings.TrimSpace(o.StringValue()) != "" {
		newTitle = strings.TrimSpace(o.StringValue())
		hasInlineEdits = true
	}
	if o, exists := optMap["description"]; exists && strings.TrimSpace(o.StringValue()) != "" {
		newDesc = strings.TrimSpace(o.StringValue())
		hasInlineEdits = true
	}
	if o, exists := optMap["button_label"]; exists && strings.TrimSpace(o.StringValue()) != "" {
		newBtnLabel = strings.TrimSpace(o.StringValue())
		hasInlineEdits = true
	}

	if !hasInlineEdits {
		// Open interactive Modal pre-filled with the current embed title, description, and button label
		modalErr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseModal,
			Data: &discordgo.InteractionResponseData{
				CustomID: "modal_ticket_post_edit:" + string(panelType),
				Title:    "Update Ticket Hub Post",
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.TextInput{
								CustomID:  "post_title",
								Label:     "Embed Title",
								Style:     discordgo.TextInputShort,
								Value:     post.Title,
								Required:  true,
								MaxLength: 150,
							},
						},
					},
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.TextInput{
								CustomID:  "post_description",
								Label:     "Flow Instructions & Description",
								Style:     discordgo.TextInputParagraph,
								Value:     post.Description,
								Required:  true,
								MaxLength: 2000,
							},
						},
					},
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.TextInput{
								CustomID:  "post_button_label",
								Label:     "Button Label",
								Style:     discordgo.TextInputShort,
								Value:     post.ButtonLabel,
								Required:  true,
								MaxLength: 80,
							},
						},
					},
				},
			},
		})
		if modalErr != nil {
			h.logger.Error("failed to open ticket post edit modal", "error", modalErr)
		}
		return
	}

	if newTitle != "" {
		post.Title = newTitle
	}
	if newDesc != "" {
		post.Description = newDesc
	}
	if newBtnLabel != "" {
		post.ButtonLabel = newBtnLabel
	}

	if err := h.syncSinglePanelPostInPlace(ctx, s, guildID, post); err != nil {
		h.logger.Error("failed to update ticket panel post in-place", "error", err, "panel_type", panelType)
		h.respondEphemeral(s, i, fmt.Sprintf("❌ Failed to update `%s` post in Discord: %v", panelType, err))
		return
	}

	h.respondEphemeral(s, i, fmt.Sprintf("✅ Updated the **%s** post in <#%s> in-place without changing its channel position.", post.Title, post.ChannelID))
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

// HandleModalSubmit processes resolution modal submissions ("modal_ticket_close")
// and dynamic post editor modal submissions ("modal_ticket_post_edit:<panel_type>").
func (h *TicketHandler) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()

	if strings.HasPrefix(data.CustomID, "modal_ticket_post_edit:") {
		if i.Member == nil || !h.svc.IsStaff(i.Member.Roles) {
			h.respondEphemeral(s, i, "❌ Insufficient permissions: only Staff can edit Ticket Hub posts.")
			return
		}
		rawType := strings.TrimPrefix(data.CustomID, "modal_ticket_post_edit:")
		panelType, err := domain.ParseTicketPanelType(rawType)
		if err != nil {
			h.respondEphemeral(s, i, "❌ Invalid ticket post type.")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		guildID := i.GuildID
		if guildID == "" {
			guildID = h.cfg.GuildID
		}

		post, err := h.svc.GetOrInitPanelPost(ctx, guildID, panelType)
		if err != nil || post == nil {
			h.respondEphemeral(s, i, "❌ Failed to load ticket post record.")
			return
		}

		inputs := extractModalInputs(data.Components)
		if t := strings.TrimSpace(inputs["post_title"]); t != "" {
			post.Title = t
		}
		if d := strings.TrimSpace(inputs["post_description"]); d != "" {
			post.Description = d
		}
		if b := strings.TrimSpace(inputs["post_button_label"]); b != "" {
			post.ButtonLabel = b
		}

		if err := h.syncSinglePanelPostInPlace(ctx, s, guildID, post); err != nil {
			h.logger.Error("failed to update ticket panel post via modal", "error", err, "panel_type", panelType)
			h.respondEphemeral(s, i, fmt.Sprintf("❌ Failed to update post in-place: %v", err))
			return
		}

		h.respondEphemeral(s, i, fmt.Sprintf("✅ Updated **%s** in <#%s> in-place without changing its position!", post.Title, post.ChannelID))
		return
	}

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

// DefaultTicketHubChannelName is the default channel name created inside the Ticket category
// for the read-only Ticket & 18+ ID Verification Hub.
const DefaultTicketHubChannelName = "ticket-creation"

// EnsureTicketHub provisions (if missing) the read-only Ticket Creation Hub channel inside the
// ticket category and ensures each of the 5 ticket & 18+ ID verification flows has its own
// standalone post and embed. Existing posts are updated in-place via ChannelMessageEditComplex
// so their positions in the channel never change.
func (h *TicketHandler) EnsureTicketHub(ctx context.Context, s *discordgo.Session, guildID string) (string, error) {
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	posts, err := h.svc.ListPanelPosts(ctx, guildID)
	if err != nil {
		return "", fmt.Errorf("loading ticket panel posts: %w", err)
	}

	hubChannelID, err := h.resolveOrCreateTicketHubChannel(s, guildID, posts)
	if err != nil {
		return "", err
	}

	for _, post := range posts {
		embed := BuildSingleTicketPanelEmbed(post)
		components := BuildSingleTicketPanelButton(post)

		// If the post already has a message in the hub channel, edit it in-place so its position never changes.
		if post.ChannelID == hubChannelID && post.MessageID != "" {
			_, editErr := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
				ID:         post.MessageID,
				Channel:    hubChannelID,
				Embeds:     &[]*discordgo.MessageEmbed{embed},
				Components: &components,
			})
			if editErr == nil {
				_ = h.svc.SavePanelPost(ctx, post)
				continue
			}
			h.logger.Warn("existing ticket panel post message not editable; re-creating post",
				"panel_type", post.PanelType, "message_id", post.MessageID, "error", editErr)
		}

		sentMsg, sendErr := s.ChannelMessageSendComplex(hubChannelID, &discordgo.MessageSend{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		})
		if sendErr != nil {
			return hubChannelID, fmt.Errorf("sending ticket panel post %s: %w", post.PanelType, sendErr)
		}

		post.ChannelID = hubChannelID
		post.MessageID = sentMsg.ID
		if saveErr := h.svc.SavePanelPost(ctx, post); saveErr != nil {
			h.logger.Warn("failed to persist ticket panel post metadata", "panel_type", post.PanelType, "error", saveErr)
		}
	}

	return hubChannelID, nil
}

// syncSinglePanelPostInPlace updates a single ticket flow's post in-place in Discord without
// changing its position in the channel. If the hub channel or message has not been created yet,
// it persists the updated fields and runs EnsureTicketHub to provision all posts in order.
func (h *TicketHandler) syncSinglePanelPostInPlace(
	ctx context.Context,
	s *discordgo.Session,
	guildID string,
	post *domain.TicketPanelPost,
) error {
	if err := h.svc.SavePanelPost(ctx, post); err != nil {
		return fmt.Errorf("saving panel post configuration: %w", err)
	}

	if post.ChannelID == "" || post.MessageID == "" {
		hubChannelID, err := h.EnsureTicketHub(ctx, s, guildID)
		if err != nil {
			return err
		}
		post.ChannelID = hubChannelID
		return nil
	}

	embed := BuildSingleTicketPanelEmbed(post)
	components := BuildSingleTicketPanelButton(post)

	_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:         post.MessageID,
		Channel:    post.ChannelID,
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &components,
	})
	if err != nil {
		// If the message or channel was deleted on Discord, re-sync the hub
		h.logger.Warn("in-place edit failed, falling back to full hub sync", "panel_type", post.PanelType, "error", err)
		hubChannelID, syncErr := h.EnsureTicketHub(ctx, s, guildID)
		if syncErr != nil {
			return err
		}
		post.ChannelID = hubChannelID
	}

	return nil
}

// resolveOrCreateTicketHubChannel finds an existing Ticket Creation Hub channel in the ticket category
// or creates a new read-only channel viewable by all members.
func (h *TicketHandler) resolveOrCreateTicketHubChannel(
	s *discordgo.Session,
	guildID string,
	posts []*domain.TicketPanelPost,
) (string, error) {
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	overwrites := h.buildTicketHubOverwrites(guildID, botID)
	categoryID := h.resolveTicketCategory(s, guildID)

	// 1. Check if any stored post already points to an active channel
	for _, p := range posts {
		if p.ChannelID != "" {
			if ch, err := s.Channel(p.ChannelID); err == nil && ch != nil {
				return ch.ID, nil
			}
		}
	}

	// 2. Check if a "ticket-creation" or "create-a-ticket" channel already exists in the guild
	if channels, err := s.GuildChannels(guildID); err == nil {
		for _, ch := range channels {
			if ch.Type != discordgo.ChannelTypeGuildText {
				continue
			}
			lowerName := strings.ToLower(ch.Name)
			if lowerName == DefaultTicketHubChannelName || lowerName == "create-a-ticket" || lowerName == "open-a-ticket" {
				if categoryID == "" || ch.ParentID == categoryID {
					return ch.ID, nil
				}
			}
		}
	}

	// 3. Create the read-only Ticket Creation Hub channel inside the ticket category
	created, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:                 DefaultTicketHubChannelName,
		Type:                 discordgo.ChannelTypeGuildText,
		ParentID:             categoryID,
		Topic:                "Read-only Ticket & 18+ ID Verification Hub — Click a button below to open a private ticket.",
		Position:             0,
		PermissionOverwrites: overwrites,
	})
	if err != nil {
		return "", fmt.Errorf("creating ticket creation hub channel: %w", err)
	}

	if h.cache != nil {
		h.cache.SetChannel(created.Name, created.ID)
	}

	return created.ID, nil
}

// buildTicketHubOverwrites constructs permissions for the Ticket Creation Hub channel:
//   - Viewable by all members (@everyone, plus unverified & verified roles)
//   - Strictly read-only (SendMessages, AddReactions, Threads, and AttachFiles are denied)
//   - Interactive buttons remain usable by all members
func (h *TicketHandler) buildTicketHubOverwrites(guildID, botID string) []*discordgo.PermissionOverwrite {
	readOnlyAllow := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	readOnlyDeny := int64(
		discordgo.PermissionSendMessages |
			discordgo.PermissionAddReactions |
			discordgo.PermissionCreatePublicThreads |
			discordgo.PermissionCreatePrivateThreads |
			discordgo.PermissionSendMessagesInThreads |
			discordgo.PermissionAttachFiles,
	)

	overwrites := []*discordgo.PermissionOverwrite{
		// 1. @everyone: Viewable by all, strictly read-only (buttons only)
		{
			ID:    guildID,
			Type:  discordgo.PermissionOverwriteTypeRole,
			Allow: readOnlyAllow,
			Deny:  readOnlyDeny,
		},
	}

	// 2. Explicitly ensure both Unverified and Verified roles have read-only view access
	// even if the parent Ticket category restricts @everyone.
	seenRoles := map[string]bool{guildID: true}
	for _, memberRoleID := range []string{h.cfg.UnverifiedRoleID, h.cfg.VerifiedRoleID} {
		if memberRoleID != "" && !seenRoles[memberRoleID] {
			seenRoles[memberRoleID] = true
			overwrites = append(overwrites, &discordgo.PermissionOverwrite{
				ID:    memberRoleID,
				Type:  discordgo.PermissionOverwriteTypeRole,
				Allow: readOnlyAllow,
				Deny:  readOnlyDeny,
			})
		}
	}

	// 3. Allow Staff roles to view and manage messages
	for _, staffRoleID := range h.cfg.StaffRoleIDs {
		if staffRoleID == "" || seenRoles[staffRoleID] {
			continue
		}
		seenRoles[staffRoleID] = true
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:   staffRoleID,
			Type: discordgo.PermissionOverwriteTypeRole,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionReadMessageHistory |
				discordgo.PermissionManageMessages |
				discordgo.PermissionManageChannels,
			Deny: discordgo.PermissionSendMessages | discordgo.PermissionAddReactions,
		})
	}

	// 4. Allow the bot itself full posting and message-editing permissions
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

// BuildSingleTicketPanelEmbed constructs the standalone embed for a single ticket or ID verification post.
func BuildSingleTicketPanelEmbed(post *domain.TicketPanelPost) *discordgo.MessageEmbed {
	color := post.Color
	if color <= 0 {
		color = 0x5865F2
	}
	return &discordgo.MessageEmbed{
		Title:       post.Title,
		Description: post.Description,
		Color:       color,
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Syn Ticket Hub • Flow: %s", post.PanelType),
		},
	}
}

// BuildSingleTicketPanelButton constructs the standalone interactive button row for a single ticket or ID verification post.
func BuildSingleTicketPanelButton(post *domain.TicketPanelPost) []discordgo.MessageComponent {
	style := discordgo.ButtonStyle(post.ButtonStyle)
	if style < discordgo.PrimaryButton || style > discordgo.DangerButton {
		style = discordgo.PrimaryButton
	}

	btn := discordgo.Button{
		CustomID: post.PanelType.CustomID(),
		Label:    post.ButtonLabel,
		Style:    style,
	}
	if strings.TrimSpace(post.ButtonEmoji) != "" {
		btn.Emoji = &discordgo.ComponentEmoji{Name: strings.TrimSpace(post.ButtonEmoji)}
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{btn},
		},
	}
}

// BuildTicketPanelEmbed generates the persistent ticket creation panel embed.
func BuildTicketPanelEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: "🎫 Server Support & Report Tickets",
		Description: "Need assistance or want to report an issue?\n" +
			"Click the button below that corresponds to your request to open a private ticket.\n\n" +
			"🚨 **Report Ticket** — Report server misconduct, harassment, or rule violations\n" +
			"🐛 **Bug Ticket** — Report bot glitches or server technical bugs\n" +
			"👤 **Account Issue Ticket** — Assistance with roles, access, or account issues\n" +
			"💬 **Support Ticket** — General server questions and support\n\n" +
			"*Note: For 18+ age verification, please use the dedicated 🪪 **18+ ID Verification** panel.*",
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
				CustomID: "btn_ticket_create:report",
				Label:    "Report",
				Style:    discordgo.DangerButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "🚨"},
			},
			discordgo.Button{
				CustomID: "btn_ticket_create:support",
				Label:    "Support",
				Style:    discordgo.SuccessButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "💬"},
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
		},
	}

	return []discordgo.MessageComponent{row1, row2}
}

func (h *TicketHandler) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		h.sendDeferredFollowup(s, i, message)
	}
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

