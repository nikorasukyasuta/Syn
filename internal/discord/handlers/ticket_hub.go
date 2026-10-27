package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"syn/internal/domain"
)

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

