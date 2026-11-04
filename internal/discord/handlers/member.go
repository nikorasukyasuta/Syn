package handlers

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/discord/syncutil"
	"syn/internal/service"
)

// MemberJoinHandler automatically assigns the configured Unverified role and initial Ask To DM role
// to newly joined human members of the server.
type MemberJoinHandler struct {
	guildID           string
	unverifiedRoleID  string
	joinAskToDMRoleID string
	honeypotSvc       *service.HoneypotService
	lockMgr           *syncutil.UserLockManager
	logger            *slog.Logger
}

// NewMemberJoinHandler constructs a new MemberJoinHandler.
func NewMemberJoinHandler(guildID, unverifiedRoleID string, logger *slog.Logger) *MemberJoinHandler {
	return &MemberJoinHandler{
		guildID:          guildID,
		unverifiedRoleID: unverifiedRoleID,
		logger:           logger,
	}
}

// SetJoinAskToDMRole configures the initial "Ask To DM" role ID assigned automatically on member join.
func (h *MemberJoinHandler) SetJoinAskToDMRole(roleID string) {
	h.joinAskToDMRoleID = strings.TrimSpace(roleID)
}

// SetHoneypotService attaches the HoneypotService so quarantined spammers who leave and rejoin
// have their Spammer quarantine re-applied instead of receiving normal join roles.
func (h *MemberJoinHandler) SetHoneypotService(svc *service.HoneypotService) {
	h.honeypotSvc = svc
}

// SetLockManager attaches a UserLockManager for serializing concurrent member operations.
func (h *MemberJoinHandler) SetLockManager(lm *syncutil.UserLockManager) {
	h.lockMgr = lm
}

// HandleGuildMemberAdd receives Discord's GUILD_MEMBER_ADD event and grants the unverified role.
func (h *MemberJoinHandler) HandleGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	if m == nil || m.Member == nil || m.User == nil {
		return
	}

	if h.lockMgr != nil {
		unlock := h.lockMgr.Lock(m.User.ID)
		defer unlock()
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

	// If the member is currently in Honeypot Spammer Quarantine, re-apply the Spammer role
	// instead of normal join roles so they cannot evade quarantine by leaving and rejoining.
	if h.honeypotSvc != nil && h.honeypotSvc.GetActive(m.User.ID) != nil {
		if err := h.honeypotSvc.ReapplyQuarantine(m.GuildID, m.User.ID); err != nil {
			h.logger.Warn("failed to reapply spammer quarantine on member rejoin",
				"guild_id", m.GuildID,
				"user_id", m.User.ID,
				"error", err,
			)
		}
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
	h.assignRoleWithRetry(s, m.GuildID, m.User.ID, m.User.Username, unverifiedRole, "unverified")

	// Also assign the initial "Ask To DM" role on join (either configured by ID or discovered by name)
	askToDMRole := h.resolveJoinAskToDMRole(s, m.GuildID)
	if askToDMRole != "" && askToDMRole != unverifiedRole {
		h.assignRoleWithRetry(s, m.GuildID, m.User.ID, m.User.Username, askToDMRole, "ask_to_dm")
	}
}

func (h *MemberJoinHandler) resolveJoinAskToDMRole(s *discordgo.Session, guildID string) string {
	if roleID := strings.TrimSpace(h.joinAskToDMRoleID); roleID != "" {
		return roleID
	}
	if s == nil || guildID == "" {
		return ""
	}
	if roles, err := s.GuildRoles(guildID); err == nil {
		for _, r := range roles {
			if r != nil && NormalizeDMRoleName(r.Name) == "ask to dm" {
				return r.ID
			}
		}
	}
	return ""
}

func (h *MemberJoinHandler) assignRoleWithRetry(s *discordgo.Session, guildID, userID, username, roleID, roleLabel string) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		err := s.GuildMemberRoleAdd(guildID, userID, roleID)
		if err == nil {
			h.logger.Info("successfully assigned join role to new member",
				"guild_id", guildID,
				"user_id", userID,
				"username", username,
				"role_id", roleID,
				"role_type", roleLabel,
				"attempt", attempt,
			)
			return
		}

		lastErr = err
		var restErr *discordgo.RESTError
		if errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == 50013 {
			h.logger.Error("failed to assign join role due to Discord Role Hierarchy (50013 Missing Permissions). The Bot role must be higher than the target role in Server Settings -> Roles, and have 'Manage Roles' permission!",
				"guild_id", guildID,
				"user_id", userID,
				"username", username,
				"role_id", roleID,
				"role_type", roleLabel,
				"error", err,
			)
			return
		}

		h.logger.Warn("retrying join role assignment to new member",
			"guild_id", guildID,
			"user_id", userID,
			"role_type", roleLabel,
			"attempt", attempt,
			"error", err,
		)
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}

	h.logger.Error("failed to assign join role to new member after retries",
		"guild_id", guildID,
		"user_id", userID,
		"username", username,
		"role_id", roleID,
		"role_type", roleLabel,
		"error", lastErr,
	)
}

// SyncUnassignedMembers scans the server members and ensures all human members
// who hold neither the unverified nor any verified role (standard or ID verified) are granted the unverified role.
func (h *MemberJoinHandler) SyncUnassignedMembers(s *discordgo.Session, verifiedRoleIDs ...string) error {
	guildID := strings.TrimSpace(h.guildID)
	unverifiedRole := strings.TrimSpace(h.unverifiedRoleID)
	if guildID == "" || unverifiedRole == "" {
		return nil
	}

	verifiedSet := make(map[string]struct{}, len(verifiedRoleIDs))
	for _, id := range verifiedRoleIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			verifiedSet[trimmed] = struct{}{}
		}
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

			if h.honeypotSvc != nil {
				if h.honeypotSvc.GetActive(m.User.ID) != nil {
					continue
				}
				if spammerRole := h.honeypotSvc.Config().SpammerRoleID; spammerRole != "" {
					isQuarantined := false
					for _, r := range m.Roles {
						if r == spammerRole {
							isQuarantined = true
							break
						}
					}
					if isQuarantined {
						continue
					}
				}
			}

			hasUnverified := false
			hasVerified := false
			for _, r := range m.Roles {
				if r == unverifiedRole {
					hasUnverified = true
				}
				if _, ok := verifiedSet[r]; ok {
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
				if joinDMRole := strings.TrimSpace(h.joinAskToDMRoleID); joinDMRole != "" && joinDMRole != unverifiedRole {
					_ = s.GuildMemberRoleAdd(guildID, m.User.ID, joinDMRole)
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

// NormalizeDMRoleName normalizes a Discord role name for DM preference comparison
// (e.g. "DM's Open" -> "dms open", "Ask To DM" -> "ask to dm", "DM’s Closed" -> "dms closed").
func NormalizeDMRoleName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.NewReplacer("'", "", "’", "", "`", "", "-", " ", "_", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// IsDMPreferenceRoleName returns true if the role name represents any of the three DM preference roles:
// "Ask To DM", "DM's Open", or "DM's Closed".
func IsDMPreferenceRoleName(raw string) bool {
	norm := NormalizeDMRoleName(raw)
	switch norm {
	case "ask to dm", "ask to dms", "ask before dm",
		"dms open", "dm open", "open dms",
		"dms closed", "dm closed", "closed dms":
		return true
	default:
		return false
	}
}

