package discord

import (
	"log/slog"
	"runtime/debug"

	"github.com/bwmarrin/discordgo"

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

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}