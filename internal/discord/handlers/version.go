package handlers

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"syn/internal/version"
)

// VersionHandler handles the /version slash command.
type VersionHandler struct{}

// NewVersionHandler creates a new VersionHandler.
func NewVersionHandler() *VersionHandler {
	return &VersionHandler{}
}

// VersionCommandDefinition defines the /version application command.
func VersionCommandDefinition() *discordgo.ApplicationCommand {
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:         "version",
		Description:  "Show the current bot version, recent updates, and source code link",
		DMPermission: &dmPermission,
	}
}

// HandleVersionCommand renders the bot's version embed, recent updates, and GitHub repository URL.
func (h *VersionHandler) HandleVersionCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := BuildVersionEmbed()

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
	if err != nil {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{embed},
		})
	}
}

// BuildVersionEmbed constructs the version and updates embed.
func BuildVersionEmbed() *discordgo.MessageEmbed {
	var changesList strings.Builder
	if len(version.History) > 0 {
		latest := version.History[0]
		for _, c := range latest.Changes {
			changesList.WriteString(fmt.Sprintf("• %s\n", c))
		}
	} else {
		changesList.WriteString("• Performance and stability enhancements\n")
	}

	return &discordgo.MessageEmbed{
		Title:       "🚀 Syn Discord Bot • System Version",
		Description: "Production Discord bot designed for server management, 18+ verification, reaction panels, and ticket support.",
		Color:       0x5865F2, // Discord Blurple
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Current Version",
				Value:  fmt.Sprintf("`%s` (%s)", version.CurrentVersion, version.ReleaseDate),
				Inline: true,
			},
			{
				Name:   "Source Code Repository",
				Value:  fmt.Sprintf("[GitHub: nikorasukyasuta/Syn](%s)", version.GitRepositoryURL),
				Inline: true,
			},
			{
				Name:   "✨ Latest Release Highlights",
				Value:  changesList.String(),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Syn Support Suite • Open Source",
		},
	}
}

