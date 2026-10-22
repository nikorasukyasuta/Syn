package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"syn/internal/domain"
	"syn/internal/service"
	"time"

	"github.com/bwmarrin/discordgo"
)

// VerifyCommandDefinition returns the Discord Slash Command specification.
func VerifyCommandDefinition() *discordgo.ApplicationCommand {
	// Setting DefaultMemberPermissions to 0 hides the command from regular members by default.
	// Server Admins can then explicitly grant visibility to Staff & Server Helper roles in Discord Server Settings.
	var defaultPerms int64 = 0
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:                     "verify-member",
		Description:              "[Staff/Helpers Only] Verify a member as 18+ and grant access role",
		DefaultMemberPermissions: &defaultPerms,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "member",
				Description: "The Discord member who completed 18+ age verification",
				Required:    true,
			},
		},
	}
}

// VerifyHandler connects Discord slash commands to the VerificationService.
type VerifyHandler struct {
	svc              *service.VerificationService
	verifiedRoleID   string
	unverifiedRoleID string
	logger           *slog.Logger
}

// NewVerifyHandler initializes the Discord command handler.
func NewVerifyHandler(svc *service.VerificationService, verifiedRoleID, unverifiedRoleID string, loggers ...*slog.Logger) *VerifyHandler {
	var l *slog.Logger
	if len(loggers) > 0 && loggers[0] != nil {
		l = loggers[0]
	} else {
		l = slog.Default()
	}

	return &VerifyHandler{
		svc:              svc,
		verifiedRoleID:   verifiedRoleID,
		unverifiedRoleID: unverifiedRoleID,
		logger:           l,
	}
}

// HandleVerifyCommand processes the /verify-member slash command.
// If the member holds the Unverified role, it is removed and replaced with the 18+ Verified role.
func (h *VerifyHandler) HandleVerifyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, ok := i.Data.(discordgo.ApplicationCommandInteractionData)
	if !ok || len(data.Options) == 0 {
		respondEphemeral(s, i, "Target user is required.")
		return
	}
	targetUser := data.Options[0].UserValue(s)
	if targetUser == nil {
		respondEphemeral(s, i, "Could not resolve target member.")
		return
	}

	// Pass the caller's Discord Role IDs (i.Member.Roles) to the service for secondary verification.
	_, err := h.svc.VerifyMember(ctx, i.GuildID, i.Member.User.ID, i.Member.Roles, targetUser.ID)
	if err != nil && !errors.Is(err, domain.ErrAlreadyVerified) {
		respondEphemeral(s, i, fmt.Sprintf("Verification denied: %v", err.Error()))
		return
	}

	// Check if the target member currently holds the Unverified role
	var hasUnverified bool
	if h.unverifiedRoleID != "" {
		if i.Member != nil && i.Member.User != nil && i.Member.User.ID == targetUser.ID {
			for _, r := range i.Member.Roles {
				if r == h.unverifiedRoleID {
					hasUnverified = true
					break
				}
			}
		} else if targetMember, mErr := s.GuildMember(i.GuildID, targetUser.ID); mErr == nil && targetMember != nil {
			for _, r := range targetMember.Roles {
				if r == h.unverifiedRoleID {
					hasUnverified = true
					break
				}
			}
		}
	}

	// 1. Remove the Unverified role from the member
	if h.unverifiedRoleID != "" {
		if rErr := s.GuildMemberRoleRemove(i.GuildID, targetUser.ID, h.unverifiedRoleID); rErr != nil {
			h.logger.Warn("failed to remove unverified role in discord",
				"guild_id", i.GuildID,
				"user_id", targetUser.ID,
				"role_id", h.unverifiedRoleID,
				"error", rErr,
			)
		} else {
			h.logger.Info("removed unverified role from member",
				"guild_id", i.GuildID,
				"user_id", targetUser.ID,
				"role_id", h.unverifiedRoleID,
			)
		}
	}

	// 2. Replace with the 18+ Verified member role in Discord
	if h.verifiedRoleID != "" {
		if aErr := s.GuildMemberRoleAdd(i.GuildID, targetUser.ID, h.verifiedRoleID); aErr != nil {
			h.logger.Error("failed to assign verified role in discord",
				"guild_id", i.GuildID,
				"user_id", targetUser.ID,
				"role_id", h.verifiedRoleID,
				"error", aErr,
			)
			respondEphemeral(s, i, "Database updated, but failed to assign Discord verified role. Check bot role hierarchy.")
			return
		}
		h.logger.Info("assigned verified role to member",
			"guild_id", i.GuildID,
			"user_id", targetUser.ID,
			"role_id", h.verifiedRoleID,
		)
	}

	if hasUnverified {
		respondEphemeral(s, i, fmt.Sprintf("✅ Successfully verified <@%s>: removed Unverified role and granted 18+ Verified role.", targetUser.ID))
	} else {
		respondEphemeral(s, i, fmt.Sprintf("✅ Successfully verified <@%s> and granted 18+ Verified role.", targetUser.ID))
	}
}

func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, message string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
