package handlers

import (
	"bytes"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/cache"
	"syn/internal/service"
)

// TemplateHandler manages Admin-only server template export (Production -> JSON file)
// and server template state provisioning (JSON file -> local Development server).
type TemplateHandler struct {
	svc    *service.ServerTemplateService
	cfg    *config.Config
	cache  *cache.ResourceCache
	logger *slog.Logger
}

// NewTemplateHandler constructs a new TemplateHandler.
func NewTemplateHandler(
	svc *service.ServerTemplateService,
	cfg *config.Config,
	resCache *cache.ResourceCache,
	logger *slog.Logger,
) *TemplateHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &TemplateHandler{
		svc:    svc,
		cfg:    cfg,
		cache:  resCache,
		logger: logger,
	}
}

// TemplateExportCommandDefinition returns the Discord Slash Command schema for /template-export (Admins Only).
func TemplateExportCommandDefinition() *discordgo.ApplicationCommand {
	adminPerms := int64(discordgo.PermissionAdministrator)
	dmPerm := false

	return &discordgo.ApplicationCommand{
		Name:                     "template-export",
		Description:              "[Admins Only] Export the server's roles, categories, channels, and permissions to a template file",
		DefaultMemberPermissions: &adminPerms,
		DMPermission:             &dmPerm,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "file_path",
				Description: "Optional custom path to save the template JSON (defaults to config/server_template.json)",
				Required:    false,
			},
		},
	}
}

// TemplateApplyCommandDefinition returns the Discord Slash Command schema for /template-apply (Admins Only, Dev Mode Only).
func TemplateApplyCommandDefinition() *discordgo.ApplicationCommand {
	adminPerms := int64(discordgo.PermissionAdministrator)
	dmPerm := false

	return &discordgo.ApplicationCommand{
		Name:                     "template-apply",
		Description:              "[Admins Only • Dev Only] Apply a saved server template to provision roles, channels, and permissions",
		DefaultMemberPermissions: &adminPerms,
		DMPermission:             &dmPerm,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "confirm",
				Description: "Set to True to confirm provisioning roles, categories, channels, and permissions on this dev server",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "file_path",
				Description: "Optional custom path to load the template JSON from (defaults to config/server_template.json)",
				Required:    false,
			},
		},
	}
}

// HandleExportCommand processes /template-export (Admins Only).
func (h *TemplateHandler) HandleExportCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !h.isCallerAdmin(s, i) {
		respondEphemeral(s, i, "⛔ Access Denied: Only **Server Administrators** can export server templates.")
		return
	}

	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}
	if guildID == "" {
		respondEphemeral(s, i, "❌ This command can only be executed inside a Discord server.")
		return
	}

	targetPath := "config/server_template.json"
	if h.cfg != nil && h.cfg.ServerTemplatePath != "" {
		targetPath = h.cfg.ServerTemplatePath
	}

	if data, ok := i.Data.(discordgo.ApplicationCommandInteractionData); ok {
		for _, opt := range data.Options {
			if opt.Name == "file_path" && strings.TrimSpace(opt.StringValue()) != "" {
				targetPath = strings.TrimSpace(opt.StringValue())
			}
		}
	}

	// Defer ephemeral response before fetching full server state
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	guild, _ := s.Guild(guildID)
	if guild == nil && s.State != nil {
		guild, _ = s.State.Guild(guildID)
	}

	roles, err := s.GuildRoles(guildID)
	if err != nil {
		h.logger.Error("failed to fetch guild roles for template export", "guild_id", guildID, "error", err)
		h.sendFollowup(s, i, fmt.Sprintf("❌ Failed to fetch server roles: %v", err), nil, nil)
		return
	}

	channels, err := s.GuildChannels(guildID)
	if err != nil {
		h.logger.Error("failed to fetch guild channels for template export", "guild_id", guildID, "error", err)
		h.sendFollowup(s, i, fmt.Sprintf("❌ Failed to fetch server channels: %v", err), nil, nil)
		return
	}

	tmpl := h.svc.BuildTemplate(guild, roles, channels, h.cfg)
	jsonBytes, err := h.svc.SaveTemplate(targetPath, tmpl)
	if err != nil {
		h.logger.Error("failed to save server template to disk", "path", targetPath, "error", err)
		h.sendFollowup(s, i, fmt.Sprintf("❌ Failed to write server template to `%s`: %v", targetPath, err), nil, nil)
		return
	}

	h.logger.Info("exported server template successfully",
		"guild_id", guildID,
		"path", targetPath,
		"roles_count", len(tmpl.Roles),
		"categories_count", len(tmpl.Categories),
		"channels_count", len(tmpl.Channels),
	)

	embed := &discordgo.MessageEmbed{
		Title: "📦 Server Template Exported Successfully",
		Description: fmt.Sprintf(
			"Captured a complete snapshot of **%s** (`%s`) with all roles, categories, channels, and permission overwrites.\n\n"+
				"**Next Steps for Local Development:**\n"+
				"1. Download the attached `server_template.json` (or copy `%s`) into `config/server_template.json` on your local development machine.\n"+
				"2. Start the bot locally in development mode (`APP_ENV=development` or `./scripts/synctl.sh dev`).\n"+
				"3. Run `/template-apply confirm:True` on your development Discord server to recreate the exact server state.",
			tmpl.SourceGuildName,
			tmpl.SourceGuildID,
			targetPath,
		),
		Color: 0x2ECC71,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Roles Captured",
				Value:  fmt.Sprintf("`%d` roles", len(tmpl.Roles)),
				Inline: true,
			},
			{
				Name:   "Categories Captured",
				Value:  fmt.Sprintf("`%d` categories", len(tmpl.Categories)),
				Inline: true,
			},
			{
				Name:   "Channels Captured",
				Value:  fmt.Sprintf("`%d` channels", len(tmpl.Channels)),
				Inline: true,
			},
			{
				Name:   "Saved File Path",
				Value:  fmt.Sprintf("`%s`", targetPath),
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	downloadFile := &discordgo.File{
		Name:        filepath.Base(targetPath),
		ContentType: "application/json",
		Reader:      bytes.NewReader(jsonBytes),
	}

	h.sendFollowup(s, i, "", embed, []*discordgo.File{downloadFile})
}

// HandleApplyCommand processes /template-apply (Admins Only, strictly restricted to local Development mode).
func (h *TemplateHandler) HandleApplyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !h.isCallerAdmin(s, i) {
		respondEphemeral(s, i, "⛔ Access Denied: Only **Server Administrators** can apply server templates.")
		return
	}

	// Hard guard: /template-apply is strictly forbidden in Production mode or on an EC2 instance
	if h.cfg == nil || !h.cfg.IsDevelopment() || config.IsRunningOnEC2() {
		h.logger.Warn("blocked attempt to run /template-apply outside local development mode",
			"guild_id", i.GuildID,
			"environment", func() string {
				if h.cfg != nil {
					return h.cfg.Environment
				}
				return "unknown"
			}(),
		)
		respondEphemeral(s, i, "⛔ **Security Block**: `/template-apply` can **only** be executed when the bot is running locally in **Development** mode (`APP_ENV=development`) and is strictly disabled in Production / on AWS EC2.")
		return
	}

	var confirmed bool
	targetPath := h.cfg.ServerTemplatePath
	if targetPath == "" {
		targetPath = "config/server_template.json"
	}

	if data, ok := i.Data.(discordgo.ApplicationCommandInteractionData); ok {
		for _, opt := range data.Options {
			switch opt.Name {
			case "confirm":
				confirmed = opt.BoolValue()
			case "file_path":
				if strings.TrimSpace(opt.StringValue()) != "" {
					targetPath = strings.TrimSpace(opt.StringValue())
				}
			}
		}
	}

	if !confirmed {
		respondEphemeral(s, i, "⚠️ Please set `confirm: True` to confirm applying the server template to this development server.")
		return
	}

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}
	if guildID == "" {
		respondEphemeral(s, i, "❌ This command can only be executed inside a Discord server.")
		return
	}

	tmpl, err := h.svc.LoadTemplate(targetPath)
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("❌ Failed to load server template from `%s`: %v", targetPath, err))
		return
	}

	// Safety guard: never apply a template onto the exact same Production Guild ID it was exported from
	if tmpl.SourceGuildID != "" && tmpl.SourceGuildID == guildID {
		respondEphemeral(s, i, "⛔ **Safety Guard**: Target server (`"+guildID+"`) has the same Guild ID as the source production server in the template. `/template-apply` must only be run on a separate development server.")
		return
	}

	// Defer interaction response since creating roles and channels across Discord's REST API takes several seconds
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	existingRoles, _ := s.GuildRoles(guildID)
	usedExistingRoleIDs := make(map[string]bool)
	roleIDMap := make(map[string]string, len(tmpl.Roles))

	rolesCreated := 0
	rolesUpdated := 0

	// 1. Provision or sync all Roles (preserving duplicate-named roles with different colors)
	for _, rTmpl := range tmpl.Roles {
		if rTmpl.IsEveryone {
			roleIDMap[rTmpl.OriginalID] = guildID
			_, err := s.GuildRoleEdit(guildID, guildID, &discordgo.RoleParams{
				Permissions: &rTmpl.Permissions,
			})
			if err == nil {
				rolesUpdated++
			}
			continue
		}

		var matchedRole *discordgo.Role
		for _, ex := range existingRoles {
			if ex == nil || ex.Managed || ex.ID == guildID || usedExistingRoleIDs[ex.ID] {
				continue
			}
			if strings.EqualFold(ex.Name, rTmpl.Name) && ex.Color == rTmpl.Color {
				matchedRole = ex
				break
			}
		}

		params := &discordgo.RoleParams{
			Name:        rTmpl.Name,
			Color:       &rTmpl.Color,
			Hoist:       &rTmpl.Hoist,
			Permissions: &rTmpl.Permissions,
			Mentionable: &rTmpl.Mentionable,
		}

		if matchedRole != nil {
			usedExistingRoleIDs[matchedRole.ID] = true
			roleIDMap[rTmpl.OriginalID] = matchedRole.ID
			if _, err := s.GuildRoleEdit(guildID, matchedRole.ID, params); err == nil {
				rolesUpdated++
			}
		} else {
			createdRole, err := s.GuildRoleCreate(guildID, params)
			if err != nil {
				h.logger.Warn("failed to create role from template", "role_name", rTmpl.Name, "error", err)
				continue
			}
			usedExistingRoleIDs[createdRole.ID] = true
			roleIDMap[rTmpl.OriginalID] = createdRole.ID
			rolesCreated++
		}
	}

	// Refresh roles list after creation for fallback overwrite resolution
	updatedGuildRoles, err := s.GuildRoles(guildID)
	if err != nil {
		updatedGuildRoles = existingRoles
	}

	existingChannels, _ := s.GuildChannels(guildID)
	usedExistingChannelIDs := make(map[string]bool)
	categoryIDMap := make(map[string]string, len(tmpl.Categories))
	channelIDMap := make(map[string]string, len(tmpl.Categories)+len(tmpl.Channels))

	categoriesCreated := 0
	categoriesUpdated := 0

	// 2. Provision or sync all Categories
	for _, catTmpl := range tmpl.Categories {
		overwrites := h.svc.TranslateOverwrites(catTmpl.Overwrites, guildID, roleIDMap, updatedGuildRoles)

		var matchedCat *discordgo.Channel
		for _, ex := range existingChannels {
			if ex == nil || ex.Type != discordgo.ChannelTypeGuildCategory || usedExistingChannelIDs[ex.ID] {
				continue
			}
			if strings.EqualFold(ex.Name, catTmpl.Name) {
				matchedCat = ex
				break
			}
		}

		if matchedCat != nil {
			usedExistingChannelIDs[matchedCat.ID] = true
			categoryIDMap[catTmpl.OriginalID] = matchedCat.ID
			channelIDMap[catTmpl.OriginalID] = matchedCat.ID
			_, _ = s.ChannelEditComplex(matchedCat.ID, &discordgo.ChannelEdit{
				Name:                 catTmpl.Name,
				Position:             &catTmpl.Position,
				NSFW:                 &catTmpl.NSFW,
				PermissionOverwrites: overwrites,
			})
			categoriesUpdated++
		} else {
			createdCat, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
				Name:                 catTmpl.Name,
				Type:                 discordgo.ChannelTypeGuildCategory,
				Position:             catTmpl.Position,
				NSFW:                 catTmpl.NSFW,
				PermissionOverwrites: overwrites,
			})
			if err != nil {
				h.logger.Warn("failed to create category from template", "category_name", catTmpl.Name, "error", err)
				continue
			}
			usedExistingChannelIDs[createdCat.ID] = true
			categoryIDMap[catTmpl.OriginalID] = createdCat.ID
			channelIDMap[catTmpl.OriginalID] = createdCat.ID
			categoriesCreated++
		}
	}

	channelsCreated := 0
	channelsUpdated := 0

	// 3. Provision or sync all Channels inside their mapped Categories
	for _, chTmpl := range tmpl.Channels {
		overwrites := h.svc.TranslateOverwrites(chTmpl.Overwrites, guildID, roleIDMap, updatedGuildRoles)
		parentID := categoryIDMap[chTmpl.ParentOriginalID]

		var matchedCh *discordgo.Channel
		for _, ex := range existingChannels {
			if ex == nil || ex.Type != chTmpl.Type || usedExistingChannelIDs[ex.ID] {
				continue
			}
			if strings.EqualFold(ex.Name, chTmpl.Name) && (ex.ParentID == parentID || chTmpl.ParentOriginalID == "") {
				matchedCh = ex
				break
			}
		}

		if matchedCh != nil {
			usedExistingChannelIDs[matchedCh.ID] = true
			channelIDMap[chTmpl.OriginalID] = matchedCh.ID
			_, _ = s.ChannelEditComplex(matchedCh.ID, &discordgo.ChannelEdit{
				Name:                 chTmpl.Name,
				Topic:                chTmpl.Topic,
				Position:             &chTmpl.Position,
				NSFW:                 &chTmpl.NSFW,
				ParentID:             parentID,
				RateLimitPerUser:     &chTmpl.RateLimitPerUser,
				Bitrate:              chTmpl.Bitrate,
				UserLimit:            chTmpl.UserLimit,
				PermissionOverwrites: overwrites,
			})
			channelsUpdated++
		} else {
			createdCh, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
				Name:                 chTmpl.Name,
				Type:                 chTmpl.Type,
				Topic:                chTmpl.Topic,
				Position:             chTmpl.Position,
				NSFW:                 chTmpl.NSFW,
				ParentID:             parentID,
				RateLimitPerUser:     chTmpl.RateLimitPerUser,
				Bitrate:              chTmpl.Bitrate,
				UserLimit:            chTmpl.UserLimit,
				PermissionOverwrites: overwrites,
			})
			if err != nil {
				h.logger.Warn("failed to create channel from template", "channel_name", chTmpl.Name, "error", err)
				continue
			}
			usedExistingChannelIDs[createdCh.ID] = true
			channelIDMap[chTmpl.OriginalID] = createdCh.ID
			channelsCreated++
		}
	}

	// 4. Write config/discord_ids.dev.json with the newly mapped Dev Server Role & Channel IDs
	devIDsPath := "config/discord_ids.dev.json"
	if h.cfg != nil && strings.TrimSpace(h.cfg.IDsFilePath) != "" && strings.Contains(filepath.Base(h.cfg.IDsFilePath), "dev") {
		devIDsPath = h.cfg.IDsFilePath
	}
	if err := h.svc.WriteDevDiscordIDsFile(devIDsPath, guildID, tmpl.BotMapping, roleIDMap, channelIDMap, h.cfg); err != nil {
		h.logger.Warn("failed to write config/discord_ids.dev.json after template apply", "error", err)
	}

	// Update in-memory configuration with newly created Dev Server Role & Channel IDs
	if h.cfg != nil {
		if id := roleIDMap[tmpl.BotMapping.VerifiedRoleOriginalID]; id != "" {
			h.cfg.VerifiedRoleID = id
		}
		if id := roleIDMap[tmpl.BotMapping.IDVerifiedRoleOriginalID]; id != "" {
			h.cfg.IDVerifiedRoleID = id
		}
		if id := roleIDMap[tmpl.BotMapping.UnverifiedRoleOriginalID]; id != "" {
			h.cfg.UnverifiedRoleID = id
		}
		if len(tmpl.BotMapping.StaffRoleOriginalIDs) > 0 {
			var mappedStaff []string
			for _, origID := range tmpl.BotMapping.StaffRoleOriginalIDs {
				if id := roleIDMap[origID]; id != "" {
					mappedStaff = append(mappedStaff, id)
				}
			}
			if len(mappedStaff) > 0 {
				h.cfg.StaffRoleIDs = mappedStaff
			}
		}
		if id := channelIDMap[tmpl.BotMapping.TicketCategoryOriginalID]; id != "" {
			h.cfg.TicketCategoryID = id
		}
		if id := channelIDMap[tmpl.BotMapping.TicketLogChannelOriginalID]; id != "" {
			h.cfg.TicketLogChannelID = id
		}
		if id := channelIDMap[tmpl.BotMapping.StaffAlertChannelOriginalID]; id != "" {
			h.cfg.StaffAlertChannelID = id
		}
	}

	// Refresh in-memory ResourceCache
	if h.cache != nil {
		_ = h.cache.SyncFromSession(s, guildID)
	}

	h.logger.Info("applied server template to development server",
		"guild_id", guildID,
		"roles_created", rolesCreated,
		"roles_updated", rolesUpdated,
		"categories_created", categoriesCreated,
		"categories_updated", categoriesUpdated,
		"channels_created", channelsCreated,
		"channels_updated", channelsUpdated,
	)

	embed := &discordgo.MessageEmbed{
		Title: "🛠️ Development Server State Provisioned",
		Description: fmt.Sprintf(
			"Successfully applied server template from **%s** (`%s`) onto this development server (`%s`).\n\n"+
				"All newly generated development role and channel IDs have been written to `config/discord_ids.dev.json`.",
			tmpl.SourceGuildName,
			targetPath,
			guildID,
		),
		Color: 0x3498DB,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Roles",
				Value:  fmt.Sprintf("`%d` created • `%d` synced", rolesCreated, rolesUpdated),
				Inline: true,
			},
			{
				Name:   "Categories",
				Value:  fmt.Sprintf("`%d` created • `%d` synced", categoriesCreated, categoriesUpdated),
				Inline: true,
			},
			{
				Name:   "Channels",
				Value:  fmt.Sprintf("`%d` created • `%d` synced", channelsCreated, channelsUpdated),
				Inline: true,
			},
			{
				Name:   "Generated Dev Config",
				Value:  "`config/discord_ids.dev.json`",
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	h.sendFollowup(s, i, "", embed, nil)
}

func (h *TemplateHandler) isCallerAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	if (i.Member.Permissions & discordgo.PermissionAdministrator) != 0 {
		return true
	}
	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}
	if s != nil && s.State != nil && guildID != "" {
		if g, err := s.State.Guild(guildID); err == nil && g != nil {
			if i.Member.User != nil && g.OwnerID != "" && g.OwnerID == i.Member.User.ID {
				return true
			}
			for _, r := range g.Roles {
				if r != nil && (r.Permissions&discordgo.PermissionAdministrator) != 0 {
					for _, mr := range i.Member.Roles {
						if mr == r.ID {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func (h *TemplateHandler) sendFollowup(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	content string,
	embed *discordgo.MessageEmbed,
	files []*discordgo.File,
) {
	params := &discordgo.WebhookParams{
		Content: content,
		Flags:   discordgo.MessageFlagsEphemeral,
		Files:   files,
	}
	if embed != nil {
		params.Embeds = []*discordgo.MessageEmbed{embed}
	}
	_, _ = s.FollowupMessageCreate(i.Interaction, true, params)
}
