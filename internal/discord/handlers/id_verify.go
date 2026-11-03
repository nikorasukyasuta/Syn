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

// NewIDVerifyHandler constructs a new IDVerifyHandler and ensures all configured
// Server Staff, Server Helper, and ID Reviewer/Checker roles are registered on the service.
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
	if svc != nil && cfg != nil {
		svc.AddAllowedRoles(cfg.IDVerifyReviewerRoleIDs()...)
	}
	return &IDVerifyHandler{
		svc:     svc,
		cfg:     cfg,
		cache:   resCache,
		lockMgr: lockMgr,
		logger:  logger,
	}
}

// IDVerifyCommandDefinition returns the Discord slash command schema for /id-verify
// (restricted to 18+ ID verification ticket channels and accessible to Server Staff, Server Helpers, and ID Reviewers).
func IDVerifyCommandDefinition() *discordgo.ApplicationCommand {
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:         "id-verify",
		Description:  "18+ ID Verification ticket review suite (only works inside #id-verify-* ticket channels)",
		DMPermission: &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "panel",
				Description: "Post the interactive 18+ ID Verification Panel (Staff/Reviewers)",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "approve",
				Description: "Approve this ticket's 18+ ID verification and grant Verified & ID Verified roles",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "member",
						Description: "Member to approve (defaults to the current id-verify ticket applicant)",
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
				Description: "Deny this ticket's 18+ ID verification request and close the channel",
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
						Description: "Member to deny (defaults to the current id-verify ticket applicant)",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    false,
					},
				},
			},
			{
				Name:        "revoke",
				Description: "Revoke a member's 18+ verification status and restore Unverified role",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "member",
						Description: "Member whose 18+ verification should be revoked (defaults to ticket applicant)",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    false,
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
				Description: "Inspect a member's 18+ verification status and active ID requests",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "member",
						Description: "Member to inspect (defaults to the current id-verify ticket applicant)",
						Type:        discordgo.ApplicationCommandOptionUser,
						Required:    false,
					},
				},
			},
		},
	}
}

// HandleIDVerifyCommand routes incoming /id-verify subcommands (Server Staff, Helpers & ID Reviewers)
// and enforces that the command is executed exclusively inside an 18+ ID verification ticket channel.
func (h *IDVerifyHandler) HandleIDVerifyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Member == nil || !h.isAuthorizedReviewer(s, i.GuildID, i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Insufficient permissions: `/id-verify` commands are restricted to Server Staff, Server Helpers, and ID Reviewers.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	isTicketChan, activeReq := h.isIDVerificationTicketChannel(ctx, s, guildID, i.ChannelID)
	if !isTicketChan {
		h.respondEphemeral(s, i, "❌ `/id-verify` can only be used inside an 18+ ID verification ticket channel (`#id-verify-*`).")
		return
	}

	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		h.respondEphemeral(s, i, "Please specify a subcommand: `/id-verify approve`, `deny`, `revoke`, `status`, or `panel`.")
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
		h.handleCommandRevoke(s, i, subCmd.Options, activeReq)
	case "status":
		h.handleCommandStatus(s, i, subCmd.Options, activeReq)
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
	activeReq *domain.IDVerificationRequest,
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

	if targetUserID == "" && activeReq != nil {
		targetUserID = activeReq.UserID
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

	// Remove 18+ ID Verified role in Discord
	idRoleID := h.idVerifiedRoleID()
	if idRoleID != "" {
		_ = s.GuildMemberRoleRemove(guildID, targetUserID, idRoleID)
	}
	// Restore Unverified role only if the member does not hold the standard Verified role
	if h.cfg.UnverifiedRoleID != "" {
		hasStandardVerified := false
		if h.cfg.VerifiedRoleID != "" && h.cfg.VerifiedRoleID != idRoleID {
			if targetMember, mErr := s.GuildMember(guildID, targetUserID); mErr == nil && targetMember != nil {
				hasStandardVerified = hasRoleID(targetMember.Roles, h.cfg.VerifiedRoleID)
			}
		}
		if !hasStandardVerified {
			_ = s.GuildMemberRoleAdd(guildID, targetUserID, h.cfg.UnverifiedRoleID)
		}
	}

	h.logIDVerifyEvent(s, guildID, "🚫 18+ Verification Revoked", 0xE74C3C, targetUserID, i.Member.User.ID, reason)
	h.respondEphemeral(s, i, fmt.Sprintf("✅ Revoked 18+ ID verification for <@%s> and removed their ID Verified role.", targetUserID))
}

func (h *IDVerifyHandler) handleCommandStatus(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	opts []*discordgo.ApplicationCommandInteractionDataOption,
	activeReq *domain.IDVerificationRequest,
) {
	var targetUserID string
	for _, opt := range opts {
		if opt.Name == "member" {
			targetUserID = extractOptionUserID(s, opt)
		}
	}

	if targetUserID == "" && activeReq != nil {
		targetUserID = activeReq.UserID
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
		if i.Member == nil || !h.isAuthorizedReviewer(s, i.GuildID, i.Member.Roles) {
			h.respondEphemeral(s, i, "❌ Insufficient permissions: Only Server Staff, Server Helpers, and ID Reviewers can approve ID verifications.")
			return true
		}
		targetUserID := strings.TrimPrefix(customID, "btn_id_verify_approve:")
		h.executeApprove(s, i, targetUserID, i.ChannelID, "Approved via ID verification review panel", true)
		return true
	}

	if strings.HasPrefix(customID, "btn_id_verify_deny:") {
		if i.Member == nil || !h.isAuthorizedReviewer(s, i.GuildID, i.Member.Roles) {
			h.respondEphemeral(s, i, "❌ Insufficient permissions: Only Server Staff, Server Helpers, and ID Reviewers can deny ID verifications.")
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

	if i.Member == nil || !h.isAuthorizedReviewer(s, i.GuildID, i.Member.Roles) {
		h.respondEphemeral(s, i, "❌ Insufficient permissions: Only Server Staff, Server Helpers, and ID Reviewers can deny ID verifications.")
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

	// 2. Check if user already holds the dedicated ID Verified role or has an open ID verification request
	idRoleID := h.idVerifiedRoleID()
	if idRoleID != "" && hasRoleID(i.Member.Roles, idRoleID) {
		h.sendDeferredFollowup(s, i, "✅ You are already **18+ ID Verified** in this server!")
		return
	}

	existingReq, err := h.svc.CheckCanOpenRequest(ctx, guildID, caller.ID)
	if errors.Is(err, domain.ErrIDVerifyRequestAlreadyOpen) && existingReq != nil {
		h.sendDeferredFollowup(s, i, fmt.Sprintf(
			"⚠️ You already have an open 18+ ID verification session in <#%s>. Please upload your verification photo there.",
			existingReq.ChannelID,
		))
		return
	}
	if errors.Is(err, domain.ErrAlreadyVerified) {
		// If a separate IDVerifiedRoleID is configured and the member only completed basic verification (/verify-member),
		// allow them to proceed to obtain their dedicated 18+ ID Verified role.
		if h.cfg.IDVerifiedRoleID == "" || h.cfg.IDVerifiedRoleID == h.cfg.VerifiedRoleID || hasRoleID(i.Member.Roles, h.cfg.IDVerifiedRoleID) {
			h.sendDeferredFollowup(s, i, "✅ You are already **18+ ID Verified** in this server!")
			return
		}
	} else if err != nil {
		h.logger.Error("error validating ID verification eligibility", "error", err, "user_id", caller.ID)
		h.sendDeferredFollowup(s, i, "❌ An error occurred while validating your verification status. Please try again.")
		return
	}

	// 3. Resolve parent category
	categoryID := h.resolveVerificationCategory(s, guildID)

	// 4. Construct permission overwrites: Server Staff + Server Helpers + ID Reviewers + Applicant + Bot
	botID := ""
	if s.State != nil && s.State.User != nil {
		botID = s.State.User.ID
	}
	reviewerRoleIDs := h.resolveReviewerRoleIDs(s, guildID)
	overwrites := h.buildChannelOverwritesWithRoles(guildID, caller.ID, botID, reviewerRoleIDs)

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
		Topic:                fmt.Sprintf("18+ ID Verification for %s (%s) — Staff, Helpers & ID Reviewers", caller.Username, caller.ID),
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

	// 8. Post instructions embed and reviewer buttons in the new channel
	h.sendVerificationChannelWelcome(s, createdChannel.ID, caller.ID, req, reviewerRoleIDs)

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

	// 2. Grant standard Verified role so the member always receives base verification as well
	if h.cfg.VerifiedRoleID != "" {
		if aErr := s.GuildMemberRoleAdd(guildID, ver.DiscordUserID, h.cfg.VerifiedRoleID); aErr != nil {
			h.logger.Error("failed to assign verified role on ID approve",
				"guild_id", guildID,
				"user_id", ver.DiscordUserID,
				"role_id", h.cfg.VerifiedRoleID,
				"error", aErr,
			)
			h.respondEphemeral(s, i, "⚠️ Database updated, but failed to assign Discord Verified role. Check bot role hierarchy.")
			return
		}
	}

	// 3. Grant dedicated 18+ ID Verified role
	idRoleID := h.idVerifiedRoleID()
	if idRoleID != "" && idRoleID != h.cfg.VerifiedRoleID {
		if aErr := s.GuildMemberRoleAdd(guildID, ver.DiscordUserID, idRoleID); aErr != nil {
			h.logger.Error("failed to assign ID verified role on ID approve",
				"guild_id", guildID,
				"user_id", ver.DiscordUserID,
				"role_id", idRoleID,
				"error", aErr,
			)
			h.respondEphemeral(s, i, "⚠️ Database updated, but failed to assign Discord ID Verified role. Check bot role hierarchy.")
			return
		}
	}

	reviewNotes := notes
	if req != nil && req.ReviewReason != "" {
		reviewNotes = req.ReviewReason
	}

	// 4. Log approval to staff log channel
	h.logIDVerifyEvent(s, guildID, "✅ 18+ ID Verification Approved", 0x2ECC71, ver.DiscordUserID, i.Member.User.ID, reviewNotes)

	// 5. If associated with an id-verify channel, notify and purge channel in 5 seconds so ID photos are never retained
	if req != nil && req.ChannelID != "" {
		approveEmbed := &discordgo.MessageEmbed{
			Title: "✅ 18+ ID Verification Approved",
			Description: fmt.Sprintf(
				"<@%s> has been verified as **18+** by <@%s> and granted the **Verified** and **ID Verified** roles!\n\n"+
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
			h.respondEphemeral(s, i, fmt.Sprintf("✅ Approved 18+ ID verification for <@%s> (granted Verified & ID Verified roles) and scheduled <#%s> for deletion.", ver.DiscordUserID, req.ChannelID))
		}

		chToDelete := req.ChannelID
		time.AfterFunc(5*time.Second, func() {
			if _, err := s.ChannelDelete(chToDelete); err != nil {
				h.logger.Warn("failed to auto-delete approved ID verification channel", "channel_id", chToDelete, "error", err)
			}
		})
		return
	}

	h.respondEphemeral(s, i, fmt.Sprintf("✅ Successfully approved 18+ ID verification for <@%s> and granted the Verified and ID Verified roles.", ver.DiscordUserID))
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
	reviewerRoleIDs []string,
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
				"🔒 **Privacy Guarantee:** Only Server Staff, Server Helpers, and ID Reviewers can view this channel. As soon as your review is approved or closed, **this channel and your uploaded photo are permanently deleted**.",
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
				Value:  "🟡 Pending ID Review",
				Inline: true,
			},
			{
				Name:   "Reviewer Controls",
				Value:  "Server Staff, Helpers & ID Reviewers: use the buttons below or `/id-verify approve` / `/id-verify deny`.",
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Security Suite • 18+ ID Verification",
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

	var rolePings strings.Builder
	for _, roleID := range reviewerRoleIDs {
		if roleID != "" {
			rolePings.WriteString(fmt.Sprintf("<@&%s> ", roleID))
		}
	}
	content := strings.TrimSpace(fmt.Sprintf("<@%s> %s", userID, strings.TrimSpace(rolePings.String())))

	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:    content,
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
	var reviewerRoleIDs []string
	if h.cfg != nil {
		reviewerRoleIDs = h.cfg.IDVerifyReviewerRoleIDs()
	}
	return h.buildChannelOverwritesWithRoles(guildID, userID, botID, reviewerRoleIDs)
}

func (h *IDVerifyHandler) buildChannelOverwritesWithRoles(guildID, userID, botID string, reviewerRoleIDs []string) []*discordgo.PermissionOverwrite {
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

	// Grant ViewChannel & review permissions to Server Staff, Server Helpers, AND ID Reviewers/Checkers
	seen := map[string]bool{guildID: true}
	for _, reviewerRoleID := range reviewerRoleIDs {
		trimmed := strings.TrimSpace(reviewerRoleID)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:   trimmed,
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

// resolveReviewerRoleIDs returns the deduplicated list of all role IDs authorized to view and
// review 18+ ID verification tickets: configured Server Staff, Server Helpers, and ID Reviewers/Checkers,
// plus any matching role discovered in the guild or resource cache.
func (h *IDVerifyHandler) resolveReviewerRoleIDs(s *discordgo.Session, guildID string) []string {
	var result []string
	seen := make(map[string]struct{})
	addRole := func(id string) {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; !exists {
			seen[trimmed] = struct{}{}
			result = append(result, trimmed)
		}
	}

	if h.cfg != nil {
		for _, id := range h.cfg.IDVerifyReviewerRoleIDs() {
			addRole(id)
		}
	}

	if h.cache != nil {
		for _, roleName := range []string{
			"id reviewer", "id reviewers", "id-reviewer", "id-reviewers",
			"id checker", "id checkers", "id-checker", "id-checkers",
			"server helper", "server helpers", "helper", "helpers",
			"staff", "server staff",
		} {
			if id, ok := h.cache.GetRoleID(roleName); ok {
				addRole(id)
			}
		}
	}

	if s != nil && guildID != "" {
		if guildRoles, err := s.GuildRoles(guildID); err == nil {
			for _, gr := range guildRoles {
				if gr != nil && isIDVerifyReviewerRoleName(gr.Name) {
					addRole(gr.ID)
				}
			}
		}
	}

	if h.svc != nil && len(result) > 0 {
		h.svc.AddAllowedRoles(result...)
	}

	return result
}

func (h *IDVerifyHandler) isAuthorizedReviewer(s *discordgo.Session, guildID string, memberRoles []string) bool {
	if h.cfg != nil && h.svc != nil {
		h.svc.AddAllowedRoles(h.cfg.IDVerifyReviewerRoleIDs()...)
	}
	if h.svc != nil && h.svc.IsStaff(memberRoles) {
		return true
	}
	allowed := h.resolveReviewerRoleIDs(s, guildID)
	for _, mr := range memberRoles {
		for _, ar := range allowed {
			if mr == ar {
				return true
			}
		}
	}
	return false
}

// isIDVerificationTicketChannel checks whether channelID is an 18+ ID verification ticket channel
// (either recorded in the id_verification_requests table or a Discord channel named "id-verify-*").
func (h *IDVerifyHandler) isIDVerificationTicketChannel(
	ctx context.Context,
	s *discordgo.Session,
	guildID, channelID string,
) (bool, *domain.IDVerificationRequest) {
	if strings.TrimSpace(channelID) == "" {
		return false, nil
	}

	if h.svc != nil {
		if req, err := h.svc.GetRequestByChannel(ctx, guildID, channelID); err == nil && req != nil {
			return true, req
		}
	}

	if s != nil {
		if ch, err := s.Channel(channelID); err == nil && ch != nil {
			lowerName := strings.ToLower(strings.TrimSpace(ch.Name))
			if strings.HasPrefix(lowerName, "id-verify-") {
				return true, nil
			}
		}
	}

	return false, nil
}

func isIDVerifyReviewerRoleName(name string) bool {
	norm := strings.ToLower(strings.TrimSpace(name))
	norm = strings.ReplaceAll(norm, "_", " ")
	norm = strings.ReplaceAll(norm, "-", " ")
	norm = strings.Join(strings.Fields(norm), " ")
	switch norm {
	case "id reviewer", "id reviewers", "id checker", "id checkers",
		"id verifier", "id verifiers", "server helper", "server helpers",
		"server staff", "staff":
		return true
	}
	return false
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

// idVerifiedRoleID returns the dedicated IDVerifiedRoleID if configured,
// falling back to VerifiedRoleID if IDVerifiedRoleID is omitted.
func (h *IDVerifyHandler) idVerifiedRoleID() string {
	if h.cfg != nil && h.cfg.IDVerifiedRoleID != "" {
		return h.cfg.IDVerifiedRoleID
	}
	if h.cfg != nil {
		return h.cfg.VerifiedRoleID
	}
	return ""
}

func hasRoleID(roles []string, target string) bool {
	if target == "" {
		return false
	}
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}
