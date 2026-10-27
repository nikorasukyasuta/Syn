package handlers

import (
	"context"
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
	svc    *service.TicketService
	cfg    *config.Config
	cache  *cache.ResourceCache
	logger *slog.Logger
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
