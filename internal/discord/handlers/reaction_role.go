package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"syn/internal/domain"
	"syn/internal/service"
	"syn/internal/storage"

	"github.com/bwmarrin/discordgo"

)

// ReactionRoleHandler manages panel post creation, role linking, and live reaction events.
type ReactionRoleHandler struct {
	svc    *service.ReactionRoleService
	logger *slog.Logger
}

// NewReactionRoleHandler constructs a new ReactionRoleHandler.
func NewReactionRoleHandler(svc *service.ReactionRoleService, logger *slog.Logger) *ReactionRoleHandler {
	return &ReactionRoleHandler{svc: svc, logger: logger}
}

// HandlePanelCommand routes /reaction-panel subcommands (create, set-image, add-role, remove-role).
func (h *ReactionRoleHandler) HandlePanelCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok || len(data.Options) == 0 {
		return
	}

	sub := data.Options[0]
	switch sub.Name {
	case "create":
		h.handleCreatePanel(s, i, sub.Options)
	case "set-image":
		h.handleSetImage(s, i, sub.Options)
	case "add-role":
		h.handleAddRole(s, i, sub.Options)
	case "remove-role":
		h.handleRemoveRole(s, i, sub.Options)
	}
}

// handleAddRole links an emoji and role to the panel message, edits the Embed, and seeds the reaction.
func (h *ReactionRoleHandler) handleAddRole(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	optMap := optionsToMap(opts)
	messageID := strings.TrimSpace(optMap["message_id"].StringValue())
	rawEmoji := strings.TrimSpace(optMap["emoji"].StringValue())

	var roleID string
	if rOpt, ok := optMap["role"]; ok {
		if r := rOpt.RoleValue(s, i.GuildID); r != nil {
			roleID = r.ID
		} else {
			roleID = rOpt.StringValue()
		}
	}
	if roleID == "" {
		respondEphemeral(s, i, "❌ Could not resolve target role.")
		return
	}

	label := ""
	if lOpt, ok := optMap["label"]; ok {
		label = strings.TrimSpace(lOpt.StringValue())
	}

	requiresVerified := true
	if vOpt, ok := optMap["requires_verified"]; ok {
		requiresVerified = vOpt.BoolValue()
	}

	// Verify the target message exists and was authored by the bot before modifying it
	targetMsg, err := s.ChannelMessage(i.ChannelID, messageID)
	if err != nil || len(targetMsg.Embeds) == 0 {
		respondEphemeral(s, i, "❌ Could not find a valid bot panel embed with that `message_id` in this channel.")
		return
	}
	if s.State != nil && s.State.User != nil && targetMsg.Author != nil && targetMsg.Author.ID != s.State.User.ID {
		respondEphemeral(s, i, "❌ That message was not created by this bot.")
		return
	}
	if s.State != nil && s.State.User != nil && targetMsg.Author != nil && targetMsg.Author.ID != s.State.User.ID {
		respondEphemeral(s, i, "❌ That message was not created by this bot.")
		return
	}

	emojiAPIName := NormalizeEmojiInput(rawEmoji)

	rr := &domain.ReactionRole{
		GuildID:          i.GuildID,
		ChannelID:        i.ChannelID,
		MessageID:        messageID,
		EmojiAPIName:     emojiAPIName,
		EmojiDisplay:     rawEmoji,
		RoleID:           roleID,
		Description:      label,
		RequiresVerified: requiresVerified,
	}

	// 1. Validate RBAC & Protected Role Denylist, then save to PostgreSQL
	if err := h.svc.CreateBinding(ctx, i.Member.User.ID, i.Member.Roles, rr); err != nil {
		respondEphemeral(s, i, fmt.Sprintf("⛔ Cannot link role: %v", err.Error()))
		return
	}

	// 2. Have the bot react to the panel post with the emoji first (validates the emoji exists)
	if err := s.MessageReactionAdd(i.ChannelID, messageID, emojiAPIName); err != nil {
		// Rollback database mapping if the emoji was invalid or bot lacks permission
		_ = h.svc.RemoveBinding(ctx, i.Member.User.ID, i.Member.Roles, i.GuildID, messageID, emojiAPIName)
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to add reaction `%s` to the message. Make sure the emoji is valid and from a server the bot is in.", rawEmoji))
		return
	}

	// 3. Rebuild the Embed post with all linked roles from PostgreSQL
	if err := h.refreshPanelEmbed(ctx, s, i.GuildID, i.ChannelID, targetMsg); err != nil {
		respondEphemeral(s, i, fmt.Sprintf("⚠️ Role linked and reaction added, but failed to update embed text: %v", err))
		return
	}

	respondEphemeral(s, i, fmt.Sprintf("✅ Added %s → <@&%s> to panel `%s` (`18+ Required: %t`).", rawEmoji, roleID, messageID, requiresVerified))
}

// handleRemoveRole unlinks an emoji from the panel, removes the reaction, and refreshes the Embed post.
func (h *ReactionRoleHandler) handleRemoveRole(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	optMap := optionsToMap(opts)
	messageID := strings.TrimSpace(optMap["message_id"].StringValue())
	rawEmoji := strings.TrimSpace(optMap["emoji"].StringValue())
	emojiAPIName := NormalizeEmojiInput(rawEmoji)

	targetMsg, err := s.ChannelMessage(i.ChannelID, messageID)
	if err != nil || len(targetMsg.Embeds) == 0 {
		respondEphemeral(s, i, "❌ Could not find a valid bot panel embed with that `message_id` in this channel.")
		return
	}
	if s.State != nil && s.State.User != nil && targetMsg.Author != nil && targetMsg.Author.ID != s.State.User.ID {
		respondEphemeral(s, i, "❌ That message was not created by this bot.")
		return
	}

	if err := h.svc.RemoveBinding(ctx, i.Member.User.ID, i.Member.Roles, i.GuildID, messageID, emojiAPIName); err != nil {
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to remove role binding: %v", err))
		return
	}

	// Remove all instances of that emoji reaction from the message
	_ = s.MessageReactionsRemoveEmoji(i.ChannelID, messageID, emojiAPIName)

	if err := h.refreshPanelEmbed(ctx, s, i.GuildID, i.ChannelID, targetMsg); err != nil {
		respondEphemeral(s, i, fmt.Sprintf("⚠️ Binding removed, but failed to refresh panel embed: %v", err))
		return
	}

	respondEphemeral(s, i, fmt.Sprintf("✅ Unlinked %s and updated panel `%s`.", rawEmoji, messageID))
}

// refreshPanelEmbed queries PostgreSQL for all roles attached to the panel and edits the Discord message.
func (h *ReactionRoleHandler) refreshPanelEmbed(
	ctx context.Context,
	s *discordgo.Session,
	guildID, channelID string,
	msg *discordgo.Message,
) error {
	bindings, err := h.svc.ListMessageBindings(ctx, guildID, msg.ID)
	if err != nil {
		return err
	}

	var lines []string
	for _, b := range bindings {
		badge := ""
		if b.RequiresVerified {
			badge = " *(18+ Verified)*"
		}
		if b.Description != "" {
			lines = append(lines, fmt.Sprintf("%s — <@&%s> • %s%s", b.EmojiDisplay, b.RoleID, b.Description, badge))
		} else {
			lines = append(lines, fmt.Sprintf("%s — <@&%s>%s", b.EmojiDisplay, b.RoleID, badge))
		}
	}

	fieldVal := "*No roles linked yet. Use `/reaction-panel add-role`.*"
	if len(lines) > 0 {
		fieldVal = strings.Join(lines, "\n")
	}

	embed := msg.Embeds[0]
	// Strip the initial placeholder text if present
	embed.Description = strings.ReplaceAll(
		embed.Description,
		"\n\n*No roles linked yet. Staff can add roles using `/reaction-panel add-role`.*",
		"",
	)
	embed.Fields = []*discordgo.MessageEmbedField{
		{
			Name:  "Available Roles",
			Value: fieldVal,
		},
	}

	_, err = s.ChannelMessageEditEmbed(channelID, msg.ID, embed)
	return err
}

// HandleReactionAdd grants the mapped role when a user clicks a reaction on the panel.
func (h *ReactionRoleHandler) HandleReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	if r.GuildID == "" || (s.State.User != nil && r.UserID == s.State.User.ID) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	emojiKey := r.Emoji.APIName()
	roleID, err := h.svc.ResolveReactionForAdd(ctx, r.GuildID, r.MessageID, emojiKey, r.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrReactionNotFound) {
			return
		}
		if errors.Is(err, domain.ErrUnverifiedForRole) {
			// Automatically strip the reaction if an unverified user tries to claim an 18+ role
			_ = s.MessageReactionRemove(r.ChannelID, r.MessageID, emojiKey, r.UserID)
			return
		}
		h.logger.Error("error resolving reaction add", "error", err, "user_id", r.UserID)
		return
	}

	if err := s.GuildMemberRoleAdd(r.GuildID, r.UserID, roleID); err != nil {
		h.logger.Error("failed to grant reaction role in discord",
			"error", err, "guild_id", r.GuildID, "user_id", r.UserID, "role_id", roleID,
		)
	}
}

// HandleReactionRemove revokes the mapped role when a user removes their reaction.
func (h *ReactionRoleHandler) HandleReactionRemove(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
	if r.GuildID == "" || (s.State.User != nil && r.UserID == s.State.User.ID) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	emojiKey := r.Emoji.APIName()
	roleID, err := h.svc.ResolveReactionForRemove(ctx, r.GuildID, r.MessageID, emojiKey)
	if err != nil {
		if !errors.Is(err, domain.ErrReactionNotFound) {
			h.logger.Error("error resolving reaction remove", "error", err, "user_id", r.UserID)
		}
		return
	}

	if err := s.GuildMemberRoleRemove(r.GuildID, r.UserID, roleID); err != nil {
		h.logger.Error("failed to remove reaction role in discord",
			"error", err, "guild_id", r.GuildID, "user_id", r.UserID, "role_id", roleID,
		)
	}
}

func optionsToMap(opts []*discordgo.ApplicationCommandInteractionDataOption) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	m := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(opts))
	for _, o := range opts {
		m[o.Name] = o
	}
	return m
}

// NormalizeEmojiInput converts "<:rope:123456>" or "<a:spin:123456>" into "rope:123456".
func NormalizeEmojiInput(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "<a:") && strings.HasSuffix(trimmed, ">") {
		return strings.TrimSuffix(strings.TrimPrefix(trimmed, "<a:"), ">")
	}
	if strings.HasPrefix(trimmed, "<:") && strings.HasSuffix(trimmed, ">") {
		return strings.TrimSuffix(strings.TrimPrefix(trimmed, "<:"), ">")
	}
	return trimmed
}

const defaultPanelColor = 0x9B59B6 // Deep Purple

// ReactionRoleCommandDefinition defines /reaction-panel using local EC2 image filenames + Autocomplete.
func ReactionRoleCommandDefinition() *discordgo.ApplicationCommand {
	var defaultPerms int64 = 0
	dmPerm := false

	return &discordgo.ApplicationCommand{
		Name:                     "reaction-panel",
		Description:              "[Staff/Helpers Only] Generate reaction role panels using local EC2 photos",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPerm,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "create",
				Description: "Generate a new Reaction Role embed post with photos from the EC2 instance",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "title",
						Description: "Title of the panel (e.g., 'Dynamic & Kink Roles')",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "intro",
						Description: "Introductory instructions shown at the top of the post",
						Required:    true,
					},
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "banner_file",
						Description:  "Filename of the bottom banner photo on EC2 (e.g., dynamic_banner.png)",
						Required:     false,
						Autocomplete: true, // Populates dropdown from EC2 /app/assets/panels directory!
					},
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "thumbnail_file",
						Description:  "Filename of the top-right thumbnail photo on EC2 (e.g., crest.png)",
						Required:     false,
						Autocomplete: true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "color_hex",
						Description: "Optional hex color code for the embed border (e.g. #E91E63 or #9B59B6)",
						Required:    false,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "set-image",
				Description: "Swap or clear the EC2 banner/thumbnail photos on an existing panel post",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message_id",
						Description: "The Message ID of the panel post to update",
						Required:    true,
					},
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "banner_file",
						Description:  "New banner filename from the EC2 instance",
						Required:     false,
						Autocomplete: true,
					},
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "thumbnail_file",
						Description:  "New thumbnail filename from the EC2 instance",
						Required:     false,
						Autocomplete: true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "clear_images",
						Description: "Set to True to remove all photos from this panel",
						Required:    false,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add-role",
				Description: "Link an emoji and role to an existing Reaction Role panel post",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message_id",
						Description: "The Message ID of the panel post",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "emoji",
						Description: "The emoji to react with (Unicode like 🪢 or custom server emoji)",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionRole,
						Name:        "role",
						Description: "The Discord role to grant when a member reacts",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "label",
						Description: "Short description next to the role on the post (e.g. 'Shibari / Rope')",
						Required:    false,
					},
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "requires_verified",
						Description: "Require 18+ verification to claim this role? (Default: True)",
						Required:    false,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "remove-role",
				Description: "Unlink an emoji from a Reaction Role panel post and update the post",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message_id",
						Description: "The Message ID of the panel post",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "emoji",
						Description: "The emoji to unlink",
						Required:    true,
					},
				},
			},
		},
	}
}

// HandleAutocomplete dynamically lists image files from the EC2 instance as the Staff member types.
func (h *ReactionRoleHandler) HandleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	files, err := h.svc.ListEC2ImageFiles()
	if err != nil {
		h.logger.Error("failed to list EC2 images for autocomplete", "error", err)
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{
				Choices: []*discordgo.ApplicationCommandOptionChoice{},
			},
		})
		return
	}

	// Find which option the user is currently typing into
	var focusedVal string
	for _, sub := range i.ApplicationCommandData().Options {
		for _, opt := range sub.Options {
			if opt.Focused {
				focusedVal = strings.ToLower(strings.TrimSpace(opt.StringValue()))
			}
		}
	}

	var choices []*discordgo.ApplicationCommandOptionChoice
	for _, f := range files {
		if focusedVal == "" || strings.Contains(strings.ToLower(f), focusedVal) {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  f,
				Value: f,
			})
		}
		if len(choices) >= 25 { // Discord maximum autocomplete choices
			break
		}
	}

	if choices == nil {
		choices = []*discordgo.ApplicationCommandOptionChoice{}
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	})
}

// handleCreatePanel reads the local EC2 image files and uploads them inside the Discord Embed via attachment://.
func (h *ReactionRoleHandler) handleCreatePanel(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	optMap := optionsToMap(opts)
	title := strings.TrimSpace(optMap["title"].StringValue())
	intro := strings.TrimSpace(optMap["intro"].StringValue())

	var bannerFile, thumbFile string
	if bOpt, ok := optMap["banner_file"]; ok {
		bannerFile = strings.TrimSpace(bOpt.StringValue())
	}
	if tOpt, ok := optMap["thumbnail_file"]; ok {
		thumbFile = strings.TrimSpace(tOpt.StringValue())
	}

	colorVal := defaultPanelColor
	if cOpt, ok := optMap["color_hex"]; ok {
		if parsed, err := parseHexColor(cOpt.StringValue()); err == nil {
			colorVal = parsed
		}
	}

	// 1. Validate & load the image streams from the confined EC2 directory
	bannerAsset, thumbAsset, err := h.svc.LoadPanelAssets(ctx, i.Member.User.ID, i.Member.Roles, bannerFile, thumbFile)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("❌ EC2 Image Error: %v", err))
		return
	}

	panel := &domain.ReactionPanel{
		GuildID:       i.GuildID,
		ChannelID:     i.ChannelID,
		Title:         title,
		Intro:         intro,
		ColorHex:      colorVal,
		BannerFile:    bannerFile,
		ThumbnailFile: thumbFile,
	}

	embed := buildPanelEmbed(panel, nil)
	files := buildDiscordFiles(bannerAsset, thumbAsset)

	// 2. Send the Embed + Multipart EC2 Image Files to Discord
	msg, err := s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Files:  files,
	})
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to post panel with EC2 images: %v", err))
		return
	}

	// 3. Persist the panel metadata to PostgreSQL
	panel.MessageID = msg.ID
	if err := h.svc.SavePanelMetadata(ctx, i.Member.User.ID, i.Member.Roles, panel); err != nil {
		_ = s.ChannelMessageDelete(i.ChannelID, msg.ID)
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to save panel metadata: %v", err))
		return
	}

	respondEphemeral(s, i, fmt.Sprintf(
		"✅ **Reaction Role Panel Created with EC2 Photos!**\n• **Message ID:** `%s`\n• Next step: Run `/reaction-panel add-role message_id:%s` to link emojis and roles.",
		msg.ID, msg.ID,
	))
}

// handleSetImage replaces or removes the EC2 image files attached to an existing panel message.
func (h *ReactionRoleHandler) handleSetImage(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	optMap := optionsToMap(opts)
	messageID := strings.TrimSpace(optMap["message_id"].StringValue())

	var bannerFile, thumbFile string
	if bOpt, ok := optMap["banner_file"]; ok {
		bannerFile = strings.TrimSpace(bOpt.StringValue())
	}
	if tOpt, ok := optMap["thumbnail_file"]; ok {
		thumbFile = strings.TrimSpace(tOpt.StringValue())
	}

	clearImages := false
	if cOpt, ok := optMap["clear_images"]; ok {
		clearImages = cOpt.BoolValue()
	}

	if !clearImages && bannerFile == "" && thumbFile == "" {
		respondEphemeral(s, i, "⚠️ Specify a `banner_file`, `thumbnail_file`, or set `clear_images: True`.")
		return
	}

	panel, bannerAsset, thumbAsset, err := h.svc.UpdatePanelImages(
		ctx, i.Member.User.ID, i.Member.Roles,
		i.GuildID, messageID, bannerFile, thumbFile, clearImages,
	)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to update panel images: %v", err))
		return
	}

	bindings, err := h.svc.ListMessageBindings(ctx, i.GuildID, messageID)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to load panel bindings: %v", err))
		return
	}

	embed := buildPanelEmbed(panel, bindings)
	files := buildDiscordFiles(bannerAsset, thumbAsset)
	emptyAttachments := []*discordgo.MessageAttachment{} // Clears previous message attachments before uploading new ones

	_, err = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:          messageID,
		Channel:     i.ChannelID,
		Embeds:      &[]*discordgo.MessageEmbed{embed},
		Files:       files,
		Attachments: &emptyAttachments,
	})
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("⚠️ Database updated, but Discord message edit failed: %v", err))
		return
	}

	respondEphemeral(s, i, fmt.Sprintf("✅ Updated EC2 photos on panel `%s`!", messageID))
}

// buildPanelEmbed binds local EC2 filenames via Discord's "attachment://<filename>" URI scheme.
func buildPanelEmbed(panel *domain.ReactionPanel, bindings []*domain.ReactionRole) *discordgo.MessageEmbed {
	var lines []string
	for _, b := range bindings {
		badge := ""
		if b.RequiresVerified {
			badge = " *(18+ Verified)*"
		}
		if b.Description != "" {
			lines = append(lines, fmt.Sprintf("%s — <@&%s> • %s%s", b.EmojiDisplay, b.RoleID, b.Description, badge))
		} else {
			lines = append(lines, fmt.Sprintf("%s — <@&%s>%s", b.EmojiDisplay, b.RoleID, badge))
		}
	}

	fieldVal := "*No roles linked yet. Staff can add roles using `/reaction-panel add-role`.*"
	if len(lines) > 0 {
		fieldVal = strings.Join(lines, "\n")
	}

	embed := &discordgo.MessageEmbed{
		Title:       panel.Title,
		Description: panel.Intro,
		Color:       panel.ColorHex,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Available Roles",
				Value: fieldVal,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "React below to toggle roles • Unreact to remove",
		},
	}

	// Reference the uploaded multipart files using attachment://
	if panel.BannerFile != "" {
		embed.Image = &discordgo.MessageEmbedImage{
			URL: "attachment://" + panel.BannerFile,
		}
	}
	if panel.ThumbnailFile != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{
			URL: "attachment://" + panel.ThumbnailFile,
		}
	}

	return embed
}

// buildDiscordFiles converts validated EC2 AssetFiles into discordgo.File structs for upload.
func buildDiscordFiles(banner, thumb *storage.AssetFile) []*discordgo.File {
	var files []*discordgo.File
	if banner != nil {
		files = append(files, &discordgo.File{
			Name:        banner.Name,
			ContentType: banner.ContentType,
			Reader:      banner.Reader,
		})
	}
	// Avoid uploading duplicate file if the same image is used for both banner and thumbnail
	if thumb != nil && (banner == nil || thumb.Name != banner.Name) {
		files = append(files, &discordgo.File{
			Name:        thumb.Name,
			ContentType: thumb.ContentType,
			Reader:      thumb.Reader,
		})
	}
	return files
}

func parseHexColor(raw string) (int, error) {
	cleaned := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	val, err := strconv.ParseInt(cleaned, 16, 32)
	return int(val), err
}
