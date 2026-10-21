package handlers

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
)

// MemberJoinHandler automatically assigns the configured Unverified role
// to newly joined human members of the server.
type MemberJoinHandler struct {
	guildID          string
	unverifiedRoleID string
	logger           *slog.Logger
}

// NewMemberJoinHandler constructs a new MemberJoinHandler.
func NewMemberJoinHandler(guildID, unverifiedRoleID string, logger *slog.Logger) *MemberJoinHandler {
	return &MemberJoinHandler{
		guildID:          guildID,
		unverifiedRoleID: unverifiedRoleID,
		logger:           logger,
	}
}

// HandleGuildMemberAdd receives Discord's GUILD_MEMBER_ADD event and grants the unverified role.
func (h *MemberJoinHandler) HandleGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	if m == nil || m.Member == nil || m.User == nil {
		return
	}

	// Only process joins for the configured server
	if h.guildID != "" && m.GuildID != h.guildID {
		return
	}

	// Do not assign unverified roles to bot integrations
	if m.User.Bot {
		h.logger.Debug("ignoring bot account join", "bot_id", m.User.ID, "username", m.User.Username)
		return
	}

	if h.unverifiedRoleID == "" {
		h.logger.Warn("cannot assign unverified role: unverifiedRoleID is not configured")
		return
	}

	if err := s.GuildMemberRoleAdd(m.GuildID, m.User.ID, h.unverifiedRoleID); err != nil {
		h.logger.Error("failed to assign unverified role to new member",
			"guild_id", m.GuildID,
			"user_id", m.User.ID,
			"username", m.User.Username,
			"role_id", h.unverifiedRoleID,
			"error", err,
		)
		return
	}

	h.logger.Info("assigned unverified role to new member",
		"guild_id", m.GuildID,
		"user_id", m.User.ID,
		"username", m.User.Username,
		"role_id", h.unverifiedRoleID,
	)
}

