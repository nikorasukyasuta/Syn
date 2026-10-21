package handlers

import (
	"context"
	"fmt"
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
}

// NewVerifyHandler initializes the Discord command handler.
func NewVerifyHandler(svc *service.VerificationService, verifiedRoleID, unverifiedRoleID string) *VerifyHandler {
	return &VerifyHandler{
		svc:              svc,
		verifiedRoleID:   verifiedRoleID,
		unverifiedRoleID: unverifiedRoleID,
	}
}

// HandleVerifyCommand processes the /verify-member slash command.
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
	if err != nil {
		respondEphemeral(s, i, fmt.Sprintf("Verification denied: %v", err.Error()))
		return
	}

	// Assign the 18+ Verified role in Discord
	if err := s.GuildMemberRoleAdd(i.GuildID, targetUser.ID, h.verifiedRoleID); err != nil {
		respondEphemeral(s, i, "Database updated, but failed to assign Discord role. Check bot role hierarchy.")
		return
	}

	// Automatically remove the Unverified role now that member is 18+ verified
	if h.unverifiedRoleID != "" {
		_ = s.GuildMemberRoleRemove(i.GuildID, targetUser.ID, h.unverifiedRoleID)
	}

	respondEphemeral(s, i, fmt.Sprintf("✅ Successfully verified <@%s> and assigned the 18+ role.", targetUser.ID))
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
