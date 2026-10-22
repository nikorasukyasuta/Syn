package discord

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/discord/ratelimit"
)

// InteractionHandlerFunc defines the signature for Discord slash command handlers.
type InteractionHandlerFunc func(s *discordgo.Session, i *discordgo.InteractionCreate)

// RecoverPanic prevents a panic inside an interaction handler from crashing the bot process.
func RecoverPanic(logger *slog.Logger, next InteractionHandlerFunc) InteractionHandlerFunc {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("recovered from panic in discord interaction handler",
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				respondForbidden(s, i, "⚠️ An internal error occurred. The incident has been logged.")
			}
		}()
		next(s, i)
	}
}

// RequireRoles restricts command execution exclusively to members holding at least one
// of the allowed Discord Role IDs (Staff or Server Helpers).
func RequireRoles(allowedRoleIDs []string, logger *slog.Logger, next InteractionHandlerFunc) InteractionHandlerFunc {
	roleSet := make(map[string]struct{}, len(allowedRoleIDs))
	for _, id := range allowedRoleIDs {
		roleSet[id] = struct{}{}
	}

	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.GuildID == "" || i.Member == nil || i.Member.User == nil {
			respondForbidden(s, i, "This command can only be used inside the server.")
			return
		}

		if !HasAnyRole(i.Member.Roles, roleSet) {
			cmdName := ""
			if data, ok := i.Data.(discordgo.ApplicationCommandInteractionData); ok {
				cmdName = data.Name
			}

			logger.Warn("unauthorized command attempt blocked at middleware",
				"user_id", i.Member.User.ID,
				"username", i.Member.User.Username,
				"command", cmdName,
			)
			respondForbidden(s, i, "⛔ Access Denied: Only **Staff** and **Server Helpers** can run this command.")
			return
		}

		next(s, i)
	}
}

// RateLimit enforces an in-memory token bucket rate limit on users to prevent spam
// from degrading bot responsiveness or consuming Discord API rate limit buckets.
func RateLimit(
	limiter *ratelimit.UserRateLimiter,
	staffRoleIDs []string,
	exemptCmds []string,
	logger *slog.Logger,
	next InteractionHandlerFunc,
) InteractionHandlerFunc {
	if limiter == nil {
		return next
	}

	staffMap := make(map[string]struct{}, len(staffRoleIDs))
	for _, id := range staffRoleIDs {
		staffMap[id] = struct{}{}
	}

	exemptMap := make(map[string]struct{}, len(exemptCmds))
	for _, cmd := range exemptCmds {
		exemptMap[cmd] = struct{}{}
	}

	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		var userID string
		var roles []string

		if i.Member != nil && i.Member.User != nil {
			userID = i.Member.User.ID
			roles = i.Member.Roles
		} else if i.User != nil {
			userID = i.User.ID
		}

		cmdName := ""
		if data, ok := i.Data.(discordgo.ApplicationCommandInteractionData); ok {
			cmdName = data.Name
		}

		// Bypass for exempt commands (e.g. emergency safewords)
		if _, isExempt := exemptMap[cmdName]; isExempt {
			next(s, i)
			return
		}

		// Bypass rate limiting for Server Staff and Helpers
		if HasAnyRole(roles, staffMap) {
			next(s, i)
			return
		}

		allowed, wait := limiter.Allow(userID)
		if !allowed {
			waitSec := int(wait.Seconds())
			if waitSec < 1 {
				waitSec = 1
			}

			logger.Warn("user interaction rate limited",
				"user_id", userID,
				"command", cmdName,
				"retry_after_sec", waitSec,
			)

			respondForbidden(s, i, fmt.Sprintf("⏱️ **Slow down!** You are issuing commands too quickly. Please wait %d second(s) before trying again.", waitSec))
			return
		}

		next(s, i)
	}
}

// WithTimeoutGuard wraps an interaction with SLA timeout protection. If the handler
// takes longer than autoDeferDelay, the interaction is automatically acknowledged as deferred,
// extending the completion window to 15 minutes and preventing Discord timeout errors.
func WithTimeoutGuard(autoDeferDelay time.Duration, next InteractionHandlerFunc) InteractionHandlerFunc {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type == discordgo.InteractionApplicationCommand {
			_ = ratelimit.NewResponder(s, i, autoDeferDelay)
		}
		next(s, i)
	}
}

// HasAnyRole checks if any of the member's roles exist in the allowed role map.
func HasAnyRole(memberRoles []string, allowedRoleSet map[string]struct{}) bool {
	for _, roleID := range memberRoles {
		if _, ok := allowedRoleSet[roleID]; ok {
			return true
		}
	}
	return false
}

func respondForbidden(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{
				Choices: []*discordgo.ApplicationCommandOptionChoice{},
			},
		})
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		})
	}
}