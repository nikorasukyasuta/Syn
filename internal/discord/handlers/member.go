package handlers

import (
	"errors"
	"log/slog"
	"strings"
	"time"

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

	guildID := strings.TrimSpace(m.GuildID)
	cfgGuildID := strings.TrimSpace(h.guildID)

	// Only process joins for the configured server
	if cfgGuildID != "" && guildID != "" && guildID != cfgGuildID {
		return
	}

	// Do not assign unverified roles to bot integrations
	if m.User.Bot {
		h.logger.Debug("ignoring bot account join", "bot_id", m.User.ID, "username", m.User.Username)
		return
	}

	unverifiedRole := strings.TrimSpace(h.unverifiedRoleID)
	if unverifiedRole == "" {
		h.logger.Warn("cannot assign unverified role: unverifiedRoleID is not configured",
			"guild_id", m.GuildID,
			"user_id", m.User.ID,
		)
		return
	}

	h.logger.Info("new member joined server, assigning unverified role",
		"guild_id", m.GuildID,
		"user_id", m.User.ID,
		"username", m.User.Username,
		"role_id", unverifiedRole,
	)

	// Attempt role assignment with retries to withstand transient Discord API rate limits or consistency delays
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		err := s.GuildMemberRoleAdd(m.GuildID, m.User.ID, unverifiedRole)
		if err == nil {
			h.logger.Info("successfully assigned unverified role to new member",
				"guild_id", m.GuildID,
				"user_id", m.User.ID,
				"username", m.User.Username,
				"role_id", unverifiedRole,
				"attempt", attempt,
			)
			return
		}

		lastErr = err
		var restErr *discordgo.RESTError
		if errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == 50013 {
			h.logger.Error("failed to assign unverified role due to Discord Role Hierarchy (50013 Missing Permissions). The Bot role must be higher than the Unverified role in Server Settings -> Roles, and have 'Manage Roles' permission!",
				"guild_id", m.GuildID,
				"user_id", m.User.ID,
				"username", m.User.Username,
				"role_id", unverifiedRole,
				"error", err,
			)
			return
		}

		h.logger.Warn("retrying unverified role assignment to new member",
			"guild_id", m.GuildID,
			"user_id", m.User.ID,
			"attempt", attempt,
			"error", err,
		)
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}

	h.logger.Error("failed to assign unverified role to new member after retries",
		"guild_id", m.GuildID,
		"user_id", m.User.ID,
		"username", m.User.Username,
		"role_id", unverifiedRole,
		"error", lastErr,
	)
}

// SyncUnassignedMembers scans the server members and ensures all human members
// who hold neither the unverified nor verified role are granted the unverified role.
func (h *MemberJoinHandler) SyncUnassignedMembers(s *discordgo.Session, verifiedRoleID string) error {
	guildID := strings.TrimSpace(h.guildID)
	unverifiedRole := strings.TrimSpace(h.unverifiedRoleID)
	if guildID == "" || unverifiedRole == "" {
		return nil
	}

	h.logger.Info("starting unverified member role sweep", "guild_id", guildID, "unverified_role_id", unverifiedRole)

	after := ""
	assignedCount := 0
	for {
		members, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			h.logger.Warn("failed to fetch guild members for unverified sweep", "error", err)
			return err
		}
		if len(members) == 0 {
			break
		}

		for _, m := range members {
			if m.User == nil || m.User.Bot {
				continue
			}

			hasUnverified := false
			hasVerified := false
			for _, r := range m.Roles {
				if r == unverifiedRole {
					hasUnverified = true
				}
				if verifiedRoleID != "" && r == verifiedRoleID {
					hasVerified = true
				}
			}

			// If member is not verified and does not have the unverified role, assign it
			if !hasVerified && !hasUnverified {
				if err := s.GuildMemberRoleAdd(guildID, m.User.ID, unverifiedRole); err != nil {
					h.logger.Warn("sweep: failed to assign unverified role to member",
						"user_id", m.User.ID,
						"username", m.User.Username,
						"error", err,
					)
				} else {
					assignedCount++
					h.logger.Info("sweep: assigned unverified role to member",
						"user_id", m.User.ID,
						"username", m.User.Username,
					)
				}
			}
		}

		if len(members) < 1000 {
			break
		}
		after = members[len(members)-1].User.ID
	}

	h.logger.Info("unverified member role sweep completed", "guild_id", guildID, "assigned_count", assignedCount)
	return nil
}

