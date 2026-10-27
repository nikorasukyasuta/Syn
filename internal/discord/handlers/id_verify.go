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

// IDVerifyHandler manages the dedicated 18+ ID verification flow:
//   - User-initiated ID verification panel button ("btn_id_verify_start")
//   - Staff-gated slash commands (/id-verify panel, approve, deny, revoke, status)
//   - Staff-gated review buttons ("btn_id_verify_approve:<uid>", "btn_id_verify_deny:<uid>")
type IDVerifyHandler struct {
	svc     *service.IDVerificationService
	cfg     *config.Config
	cache   *cache.ResourceCache
	lockMgr *syncutil.UserLockManager
	logger  *slog.Logger
}

// NewIDVerifyHandler constructs a new IDVerifyHandler.
func NewIDVerifyHandler(
	svc *service.IDVerificationService,
	cfg *config.Config,
	resCache *cache.ResourceCache,
	lockMgr *syncutil.UserLockManager,
	logger *slog.Logger,
) *IDVerifyHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &IDVerifyHandler{
		svc:     svc,
		cfg:     cfg,
		cache:   resCache,
		lockMgr: lockMgr,
		logger:  logger,
	}
}

// IDVerifyCommandDefinition returns the Discord slash command schema for /id-verify (Staff Only).
func IDVerifyCommandDefinition() *discordgo.ApplicationCommand {
	var defaultPerms int64 = 0
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:                     "id-verify",
		Description:              "18+ ID Verification management and review suite (Staff Only)",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "panel",
				Description: "Post the interactive 18+ ID Verification Panel for members (Staff Only)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "approve",
				Description: "Approve a member's 18+ ID verification and grant Verified role (Staff Only)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "member",
						Description: "Member to approve (optional if run inside their id-verify channel)",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    false,
					},
					{
						Name:        "notes",
						Description: "Optional verification review notes",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    false,
					},
				},
			},
			{
				Name:        "deny",
				Description: "Deny a member's 18+ ID verification request and close their channel (Staff Only)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "reason",
						Description: "Reason for denying the ID verification",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
					},
					{
						Name:        "member",
						Description: "Member to deny (optional if run inside their id-verify channel)",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    false,
					},
				},
			},
			{
				Name:        "revoke",
				Description: "Revoke a member's 18+ verification status and restore Unverified role (Staff Only)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "member",
						Description: "Member whose 18+ verification should be revoked",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    true,
					},
					{
						Name:        "reason",
						Description: "Reason for revoking 18+ verification",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    false,
					},
				},
			},
			{
				Name:        "status",
				Description: "Inspect a member's 18+ verification status and active ID requests (Staff Only)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "member",
						Description: "Member to inspect",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    true,
					},
				},
			},
		},
	}
}

// HandleIDVerifyCommand routes incoming /id-verify subcommands (Staff Only).
func (h *IDVerifyHandler) HandleIDVerifyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || !h.svc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Insufficient permissions: `/id-verify` commands are restricted to Server Staff and Helpers.")
		return
	}

	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		h.respondEphemeral(s, i, "Please specify a subcommand: `/id-verify panel`, `approve`, `deny`, `revoke`, or `status`.")
		return
	}

	subCmd := options[0]
	switch subCmd.Name {
	case "panel":
		h.handleCommandPanel(s, i)
	case "approve":
		h.handleCommandApprove(s, i, subCmd.Options)
	case "deny":
		h.handleCommandDeny(s, i, subCmd.Options)
	case "revoke":
		h.handleCommandRevoke(s, i, subCmd.Options)
	case "status":
		h.handleCommandStatus(s, i, subCmd.Options)
	default:
		h.respondEphemeral(s, i, "Unknown `/id-verify` subcommand.")
	}
}

func (h *IDVerifyHandler) handleCommandPanel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := BuildIDVerifyPanelEmbed()
	components := BuildIDVerifyPanelButtons()

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	}); err != nil {
		h.logger.Error("failed to post ID verification panel", "error", err)
	}
}

func (h *IDVerifyHandler) handleCommandApprove(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	var targetUserID string
	var notes string

	for _, opt := range opts {
		switch opt.Name {
		case "member":
			targetUserID = extractOptionUserID(s, opt)
		case "notes":
			notes = opt.StringValue()
		}
	}

	h.executeApprove(s, i, targetUserID, i.ChannelID, notes, false)
}

func (h *IDVerifyHandler) handleCommandDeny(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	var targetUserID string
	var reason string

	for _, opt := range opts {
		switch opt.Name {
		case "member":
			targetUserID = extractOptionUserID(s, opt)
		case "reason":
			reason = opt.StringValue()
		}
	}

	h.executeDeny(s, i, targetUserID, i.ChannelID, reason, false)
}

func (h *IDVerifyHandler) handleCommandRevoke(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	var targetUserID string
	var reason string

	for _, opt := range opts {
		switch opt.Name {
		case "member":
			targetUserID = extractOptionUserID(s, opt)
		case "reason":
			reason = opt.StringValue()
		}
	}

	if targetUserID == "" {
		h.respondEphemeral(s, i, "❌ Please specify a valid `member` to revoke.")
		return
	}

	if h.lockMgr != nil {
		unlock := h.lockMgr.Lock(targetUserID)
		defer unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	_, err := h.svc.Revoke(ctx, guildID, i.Member.User.ID, i.Member.Roles, targetUserID, reason)
	if err != nil {
		if errors.Is(err, domain.ErrNotVerified) {
			h.respondEphemeral(s, i, fmt.Sprintf("⚠️ <@%s> is not currently marked as 18+ verified.", targetUserID))
			return
		}
		h.respondEphemeral(s, i, fmt.Sprintf("❌ Failed to revoke verification: %v", err))
		return
	}

	// Remove Verified role and restore Unverified role in Discord
	if h.cfg.VerifiedRoleID != "" {
		_ = s.GuildMemberRoleRemove(guildID, targetUserID, h.cfg.VerifiedRoleID)
	}
	if h.cfg.UnverifiedRoleID != "" {
		_ = s.GuildMemberRoleAdd(guildID, targetUserID, h.cfg.UnverifiedRoleID)
	}

	h.logIDVerifyEvent(s, guildID, "🚫 18+ Verification Revoked", 0xE74C3C, targetUserID, i.Member.User.ID, reason)
	h.respondEphemeral(s, i, fmt.Sprintf("✅ Revoked 18+ verification for <@%s>: removed Verified role and restored Unverified role.", targetUserID))
}

func (h *IDVerifyHandler) handleCommandStatus(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
) {
	var targetUserID string
	for _, opt := range opts {
		if opt.Name == "member" {
			targetUserID = extractOptionUserID(s, opt)
		}
	}

	if targetUserID == "" {
		h.respondEphemeral(s, i, "❌ Please specify a `member` to inspect.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	ver, pendingReq, err := h.svc.GetStatus(ctx, guildID, i.Member.User.ID, i.Member.Roles, targetUserID)
	if err != nil {
		h.respondEphemeral(s, i, fmt.Sprintf("❌ Failed to query verification status: %v", err))
		return
	}

	statusStr := "🔴 Unverified"
	verifiedByStr := "N/A"
	verifiedAtStr := "N/A"
	color := 0xF59E0B

	if ver != nil && ver.IsVerified {
		statusStr = "🟢 18+ Verified"
		verifiedByStr = fmt.Sprintf("<@%s>", ver.VerifiedByID)
		verifiedAtStr = fmt.Sprintf("<t:%d:F>", ver.VerifiedAt.Unix())
		color = 0x2ECC71
	}

	pendingStr := "None"
	if pendingReq != nil {
		pendingStr = fmt.Sprintf("🟡 Pending in <#%s> (opened <t:%d:R>)", pendingReq.ChannelID, pendingReq.CreatedAt.Unix())
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🪪 18+ ID Verification Status",
		Description: fmt.Sprintf("Verification record for <@%s> (`%s`):", targetUserID, targetUserID),
		Color:       color,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Verification Status",
				Value:  statusStr,
				Inline: true,
			},
			{
				Name:   "Verified By",
				Value:  verifiedByStr,
				Inline: true,
			},
			{
				Name:   "Timestamp",
				Value:  verifiedAtStr,
				Inline: true,
			},
			{
				Name:   "Active ID Session",
				Value:  pendingStr,
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

// HandleComponentInteraction processes button clicks for the ID verification system:
//   - "btn_id_verify_start" (User-initiated button on panel)
//   - "btn_id_verify_approve:<userID>" (Staff-only button inside verification channel)
//   - "btn_id_verify_deny:<userID>" (Staff-only button inside verification channel)
func (h *IDVerifyHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	customID := i.MessageComponentData().CustomID

	if customID == "btn_id_verify_start" {
		h.processStartVerification(s, i)
		return true
	}

	if strings.HasPrefix(customID, "btn_id_verify_approve:") {
		if i.Member == nil || !h.svc.IsStaff(i.Member.Roles) {
			h.respondEphemeral(s, i, "❌ Insufficient permissions: Only Server Staff and Helpers can approve ID verifications.")
			return true
		}
		targetUserID := strings.TrimPrefix(customID, "btn_id_verify_approve:")
		h.executeApprove(s, i, targetUserID, i.ChannelID, "Approved via ID verification review panel", true)
		return true
	}

	if strings.HasPrefix(customID, "btn_id_verify_deny:") {
		if i.Member == nil || !h.svc.IsStaff(i.Member.Roles) {
			h.respondEphemeral(s, i, "❌ Insufficient permissions: Only Server Staff and Helpers can deny ID verifications.")
			return true
		}
		targetUserID := strings.TrimPrefix(customID, "btn_id_verify_deny:")
		h.promptDenyModal(s, i, targetUserID)
		return true
	}

	return false
}

// HandleModalSubmit processes staff denial modal submissions ("modal_id_verify_deny:<userID>").
func (h *IDVerifyHandler) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	data := i.ModalSubmitData()
	if !strings.HasPrefix(data.CustomID, "modal_id_verify_deny:") {
		return false
	}

	if i.Member == nil || !h.svc.IsStaff(i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Insufficient permissions: Only Server Staff and Helpers can deny ID verifications.")
		return true
	}

	targetUserID := strings.TrimPrefix(data.CustomID, "modal_id_verify_deny:")
	inputs := extractModalInputs(data.Components)
	reason := strings.TrimSpace(inputs["deny_reason"])
	if reason == "" {
		reason = "ID verification requirements were not met"
	}

	h.executeDeny(s, i, targetUserID, i.ChannelID, reason, true)
	return true
}

func (h *IDVerifyHandler) processStartVerification(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || i.Member.User == nil {
		return
	}
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

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	// 2. Check if user is already verified or already has an open ID verification request
	existingReq, err := h.svc.CheckCanOpenRequest(ctx, guildID, caller.ID)
	if errors.Is(err, domain.ErrAlreadyVerified) {
		h.sendDeferredFollowup(s, i, "✅ You are already **18+ Verified** in this server!")
		return
	}
	if errors.Is(err, domain.ErrIDVerifyRequestAlreadyOpen) && existingReq != nil {
		h.sendDeferredFollowup(s, i, fmt.Sprintf(
			"⚠️ You already have an open 18+ ID verification session in <#%s>. Please upload your verification photo there.",
			existingReq.ChannelID,
		))
		return
	}
	if err != nil {
		h.logger.Error("error validating ID verification eligibility", "error", err, "user_id", caller.ID)
		h.sendDeferredFollowup(s, i, "❌ An error occurred while validating your verification status. Please try again.")
		return
	}

	// 3. Resolve parent category
	categoryID := h.resolveVerificationCategory(s, guildID)

	// 4. Construct strict permission overwrites: Staff + Applicant + Bot ONLY
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	overwrites := h.buildChannelOverwrites(guildID, caller.ID, botID)

	// 5. Generate sanitized channel name
	cleanName := channelNameSanitizer.ReplaceAllString(strings.ToLower(caller.Username), "")
	if cleanName == "" {
		cleanName = caller.ID
	}
	if len(cleanName) > 15 {
		cleanName = cleanName[:15]
	}
	channelName := fmt.Sprintf("id-verify-%s", cleanName)

	// 6. Create private ID verification channel
	createdChannel, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:                 channelName,
		Type:                 discordgo.ChannelTypeGuildText,
		ParentID:             categoryID,
		PermissionOverwrites: overwrites,
		Topic:                fmt.Sprintf("18+ ID Verification for %s (%s) — Staff Only Review", caller.Username, caller.ID),
	})
	if err != nil {
		h.logger.Error("failed to create ID verification channel", "error", err, "guild_id", guildID, "user_id", caller.ID)
		h.sendDeferredFollowup(s, i, "❌ Failed to create private verification channel. Please verify bot permissions ('Manage Channels').")
		return
	}

	// 7. Record request in database
	req, err := h.svc.CreateRequest(ctx, guildID, createdChannel.ID, caller.ID)
	if err != nil {
		h.logger.Error("failed to record ID verification request in DB", "error", err, "channel_id", createdChannel.ID)
		_, _ = s.ChannelDelete(createdChannel.ID)
		h.sendDeferredFollowup(s, i, "❌ Database error creating verification session. Please contact staff.")
		return
	}

	// 8. Post instructions embed and staff review buttons in the new channel
	h.sendVerificationChannelWelcome(s, createdChannel.ID, caller.ID, req)

	// 9. Notify user ephemerally
	h.sendDeferredFollowup(s, i, fmt.Sprintf("🪪 Your private **18+ ID Verification** channel has been opened: <#%s>", createdChannel.ID))
}

func (h *IDVerifyHandler) executeApprove(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	targetUserID, channelID, notes string,
	fromButton bool,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	// If targetUserID was not provided, attempt to resolve it from the channel's active request
	if targetUserID == "" && channelID != "" {
		if req, err := h.svc.GetRequestByChannel(ctx, guildID, channelID); err == nil && req != nil {
			targetUserID = req.UserID
		}
	}

	if targetUserID == "" {
		h.respondEphemeral(s, i, "❌ Please specify a `member` or run `/id-verify approve` inside an active `id-verify-*` channel.")
		return
	}

	if h.lockMgr != nil {
		unlock := h.lockMgr.Lock(targetUserID)
		defer unlock()
	}

	ver, req, err := h.svc.Approve(ctx, guildID, i.Member.User.ID, i.Member.Roles, targetUserID, channelID, notes)
	if err != nil {
		h.respondEphemeral(s, i, fmt.Sprintf("❌ Approval failed: %v", err))
		return
	}

	// 1. Remove Unverified role
	if h.cfg.UnverifiedRoleID != "" {
		if rErr := s.GuildMemberRoleRemove(guildID, ver.DiscordUserID, h.cfg.UnverifiedRoleID); rErr != nil {
			h.logger.Warn("failed to remove unverified role on ID approve",
				"guild_id", guildID,
				"user_id", ver.DiscordUserID,
				"error", rErr,
			)
		}
	}

	// 2. Grant 18+ Verified role
	if h.cfg.VerifiedRoleID != "" {
		if aErr := s.GuildMemberRoleAdd(guildID, ver.DiscordUserID, h.cfg.VerifiedRoleID); aErr != nil {
			h.logger.Error("failed to assign verified role on ID approve",
				"guild_id", guildID,
				"user_id", ver.DiscordUserID,
				"error", aErr,
			)
			h.respondEphemeral(s, i, "⚠️ Database updated, but failed to assign Discord Verified role. Check bot role hierarchy.")
			return
		}
	}

	reviewNotes := notes
	if req != nil && req.ReviewReason != "" {
		reviewNotes = req.ReviewReason
	}

	// 3. Log approval to staff log channel
	h.logIDVerifyEvent(s, guildID, "✅ 18+ ID Verification Approved", 0x2ECC71, ver.DiscordUserID, i.Member.User.ID, reviewNotes)

	// 4. If associated with an id-verify channel, notify and purge channel in 5 seconds so ID photos are never retained
	if req != nil && req.ChannelID != "" {
		approveEmbed := &discordgo.MessageEmbed{
			Title: "✅ 18+ ID Verification Approved",
			Description: fmt.Sprintf(
				"<@%s> has been verified as **18+** by <@%s> and granted the Verified role!\n\n"+
					"🔒 *For your privacy, this channel and all uploaded ID images will be permanently deleted in 5 seconds.*",
				ver.DiscordUserID,
				i.Member.User.ID,
			),
			Color:     0x2ECC71,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		if fromButton || req.ChannelID == i.ChannelID {
			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Embeds: []*discordgo.MessageEmbed{approveEmbed},
				},
			})
		} else {
			_, _ = s.ChannelMessageSendEmbed(req.ChannelID, approveEmbed)
			h.respondEphemeral(s, i, fmt.Sprintf("✅ Approved 18+ ID verification for <@%s> and scheduled <#%s> for deletion.", ver.DiscordUserID, req.ChannelID))
		}

		chToDelete := req.ChannelID
		time.AfterFunc(5*time.Second, func() {
			if _, err := s.ChannelDelete(chToDelete); err != nil {
				h.logger.Warn("failed to auto-delete approved ID verification channel", "channel_id", chToDelete, "error", err)
			}
		})
		return
	}

	h.respondEphemeral(s, i, fmt.Sprintf("✅ Successfully approved 18+ ID verification for <@%s> and granted Verified role.", ver.DiscordUserID))
}

func (h *IDVerifyHandler) executeDeny(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	targetUserID, channelID, reason string,
	fromModal bool,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" {
		guildID = h.cfg.GuildID
	}

	req, err := h.svc.Deny(ctx, guildID, i.Member.User.ID, i.Member.Roles, targetUserID, channelID, reason)
	if err != nil {
		if errors.Is(err, domain.ErrIDVerifyRequestNotFound) {
			h.respondEphemeral(s, i, "❌ No active pending ID verification request found for this channel or member.")
			return
		}
		if errors.Is(err, domain.ErrIDVerifyAlreadyReviewed) {
			h.respondEphemeral(s, i, "⚠️ This ID verification request has already been reviewed.")
			return
		}
		h.respondEphemeral(s, i, fmt.Sprintf("❌ Denial failed: %v", err))
		return
	}

	// Log denial to staff log channel
	h.logIDVerifyEvent(s, guildID, "❌ 18+ ID Verification Denied", 0xE74C3C, req.UserID, i.Member.User.ID, req.ReviewReason)

	denyEmbed := &discordgo.MessageEmbed{
		Title: "❌ 18+ ID Verification Denied",
		Description: fmt.Sprintf(
			"The ID verification request for <@%s> was denied by <@%s>.\n\n"+
				"**Reason:** %s\n\n"+
				"🔒 *This channel and any uploaded photos will be permanently deleted in 5 seconds.*",
			req.UserID,
			i.Member.User.ID,
			req.ReviewReason,
		),
		Color:     0xE74C3C,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	if fromModal || req.ChannelID == i.ChannelID {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{denyEmbed},
			},
		})
	} else {
		_, _ = s.ChannelMessageSendEmbed(req.ChannelID, denyEmbed)
		h.respondEphemeral(s, i, fmt.Sprintf("✅ Denied ID verification for <@%s> and scheduled <#%s> for deletion.", req.UserID, req.ChannelID))
	}

	chToDelete := req.ChannelID
	time.AfterFunc(5*time.Second, func() {
		if _, err := s.ChannelDelete(chToDelete); err != nil {
			h.logger.Warn("failed to auto-delete denied ID verification channel", "channel_id", chToDelete, "error", err)
		}
	})
}

func (h *IDVerifyHandler) promptDenyModal(s *discordgo.Session, i *discordgo.InteractionCreate, targetUserID string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "modal_id_verify_deny:" + targetUserID,
			Title:    "Deny 18+ ID Verification",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "deny_reason",
							Label:       "Reason for Denial",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "e.g. DOB was unreadable, missing handwritten username/date note...",
							Required:    true,
							MaxLength:   500,
						},
					},
				},
			},
		},
	})
	if err != nil {
		h.logger.Error("failed to display ID verification deny modal", "error", err)
	}
}

func (h *IDVerifyHandler) sendVerificationChannelWelcome(
	s *discordgo.Session,
	channelID, userID string,
	req *domain.IDVerificationRequest,
) {
	embed := &discordgo.MessageEmbed{
		Title: "🪪 18+ Age & ID Verification Session",
		Description: fmt.Sprintf(
			"Welcome <@%s>!\n\nTo complete **18+ Age Verification**, please upload **one clear photo** below containing:\n\n"+
				"1️⃣ **Government-Issued Photo ID** showing your **Date of Birth (DOB)** clearly.\n"+
				"   • *For your privacy, please blur or cover your full name, address, photo, and ID number — we only need to verify your DOB.*\n"+
				"2️⃣ **A Handwritten Physical Note** placed next to your ID showing:\n"+
				"   • Your **Discord Username**\n"+
				"   • **Today's Date**\n\n"+
				"🔒 **Privacy Guarantee:** Only Server Staff can view this channel. As soon as a staff member approves or closes this review, **this channel and your uploaded photo are permanently deleted**.",
			userID,
		),
		Color: 0x5865F2,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Applicant",
				Value:  fmt.Sprintf("<@%s>", userID),
				Inline: true,
			},
			{
				Name:   "Status",
				Value:  "🟡 Pending Staff Review",
				Inline: true,
			},
			{
				Name:   "Staff Controls",
				Value:  "Use the buttons below or `/id-verify approve` / `/id-verify deny`.",
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Security Suite • Staff-Gated 18+ ID Verification",
		},
		Timestamp: req.CreatedAt.Format(time.RFC3339),
	}

	staffControls := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: "btn_id_verify_approve:" + userID,
				Label:    "Approve (18+)",
				Style:    discordgo.SuccessButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "✅"},
			},
			discordgo.Button{
				CustomID: "btn_id_verify_deny:" + userID,
				Label:    "Deny",
				Style:    discordgo.DangerButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "❌"},
			},
		},
	}

	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:    fmt.Sprintf("<@%s>", userID),
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{staffControls},
	})
	if err != nil {
		h.logger.Error("failed to post welcome message to ID verification channel", "channel_id", channelID, "error", err)
	}
}

// BuildIDVerifyPanelEmbed generates the persistent 18+ ID Verification panel embed.
func BuildIDVerifyPanelEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: "🪪 18+ Age & ID Verification",
		Description: "This community requires **18+ Age Verification** to access adult and verified channels.\n\n" +
			"**How It Works:**\n" +
			"1. Click the **Start 18+ ID Verification** button below to open an isolated, private channel with Server Staff.\n" +
			"2. Upload a photo of your government-issued ID (**DOB visible**, all other sensitive info redacted/covered) alongside a **handwritten note** with your Discord username and today's date.\n" +
			"3. Once a staff member verifies your age, you will receive the **18+ Verified** role and the verification channel will be **permanently deleted**.",
		Color: 0x5865F2,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Security Suite • Zero ID Image Retention",
		},
	}
}

// BuildIDVerifyPanelButtons generates the interactive button row for the 18+ ID Verification panel.
func BuildIDVerifyPanelButtons() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					CustomID: "btn_id_verify_start",
					Label:    "Start 18+ ID Verification",
					Style:    discordgo.PrimaryButton,
					Emoji:    &discordgo.ComponentEmoji{Name: "🪪"},
				},
			},
		},
	}
}

func (h *IDVerifyHandler) resolveVerificationCategory(s *discordgo.Session, guildID string) string {
	if h.cfg.TicketCategoryID != "" {
		return h.cfg.TicketCategoryID
	}

	if h.cache != nil {
		for _, candidate := range []string{"verification", "id-verification", "ticket", "tickets"} {
			if id, exists := h.cache.GetChannelID(candidate); exists {
				return id
			}
		}
	}

	channels, err := s.GuildChannels(guildID)
	if err == nil {
		for _, ch := range channels {
			if ch.Type == discordgo.ChannelTypeGuildCategory {
				lowerName := strings.ToLower(ch.Name)
				if strings.Contains(lowerName, "verif") || strings.Contains(lowerName, "ticket") {
					return ch.ID
				}
			}
		}
	}

	return ""
}

func (h *IDVerifyHandler) buildChannelOverwrites(guildID, userID, botID string) []*discordgo.PermissionOverwrite {
	overwrites := []*discordgo.PermissionOverwrite{
		{
			ID:    guildID,
			Type:  discordgo.PermissionOverwriteTypeRole,
			Deny:  discordgo.PermissionViewChannel,
			Allow: 0,
		},
		{
			ID:   userID,
			Type: discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel |
				discordgo.PermissionSendMessages |
				discordgo.PermissionReadMessageHistory |
				discordgo.PermissionAttachFiles |
				discordgo.PermissionEmbedLinks,
			Deny: 0,
		},
	}

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

func (h *IDVerifyHandler) logIDVerifyEvent(
	s *discordgo.Session,
	guildID, title string,
	color int,
	targetUserID, staffUserID, notes string,
) {
	logChannelID := h.cfg.TicketLogChannelID
	if logChannelID == "" && h.cache != nil {
		if id, exists := h.cache.GetChannelID("ticket-logs"); exists {
			logChannelID = id
		} else if id, exists := h.cache.GetChannelID("ticket-log"); exists {
			logChannelID = id
		}
	}
	if logChannelID == "" {
		return
	}

	if strings.TrimSpace(notes) == "" {
		notes = "None"
	}

	embed := &discordgo.MessageEmbed{
		Title: title,
		Color: color,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Member",
				Value:  fmt.Sprintf("<@%s> (`%s`)", targetUserID, targetUserID),
				Inline: true,
			},
			{
				Name:   "Staff Reviewer",
				Value:  fmt.Sprintf("<@%s> (`%s`)", staffUserID, staffUserID),
				Inline: true,
			},
			{
				Name:   "Notes / Reason",
				Value:  notes,
				Inline: false,
			},
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_, _ = s.ChannelMessageSendEmbed(logChannelID, embed)
}

func (h *IDVerifyHandler) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
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

func (h *IDVerifyHandler) sendDeferredFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: message,
		Flags:   discordgo.MessageFlagsEphemeral,
	})
}

func extractOptionUserID(s *discordgo.Session, opt *discordgo.ApplicationCommandInteractionDataOption) string {
	if opt == nil {
		return ""
	}
	if strVal, ok := opt.Value.(string); ok && strVal != "" {
		return strVal
	}
	if u := opt.UserValue(s); u != nil {
		return u.ID
	}
	return ""
}
