package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"syn/internal/version"
)

const (
	customIDVersionFirst  = "btn_version_first:"
	customIDVersionPrev   = "btn_version_prev:"
	customIDVersionNoop   = "btn_version_noop"
	customIDVersionNext   = "btn_version_next:"
	customIDVersionLast   = "btn_version_last:"
	customIDVersionSelect = "select_version_page"
)

// VersionHandler handles the /version slash command and its interactive pagination controls.
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
		Description:  "Show the current bot version, paginated release history, and source code link (Ephemeral)",
		DMPermission: &dmPermission,
	}
}

// HandleVersionCommand ephemerally renders the bot's version embed with pagination controls for all releases.
func (h *VersionHandler) HandleVersionCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := BuildVersionPageEmbed(0)
	components := BuildVersionComponents(0)

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		})
	}
}

// HandleComponentInteraction processes pagination button clicks and version dropdown selections
// on the ephemeral /version message, updating the message in-place.
func (h *VersionHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i == nil || i.Interaction == nil {
		return false
	}
	data := i.MessageComponentData()
	customID := data.CustomID

	targetPage := -1
	switch {
	case strings.HasPrefix(customID, customIDVersionFirst):
		targetPage = parseVersionPageIndex(strings.TrimPrefix(customID, customIDVersionFirst))
	case strings.HasPrefix(customID, customIDVersionPrev):
		targetPage = parseVersionPageIndex(strings.TrimPrefix(customID, customIDVersionPrev))
	case strings.HasPrefix(customID, customIDVersionNext):
		targetPage = parseVersionPageIndex(strings.TrimPrefix(customID, customIDVersionNext))
	case strings.HasPrefix(customID, customIDVersionLast):
		targetPage = parseVersionPageIndex(strings.TrimPrefix(customID, customIDVersionLast))
	case customID == customIDVersionSelect:
		if len(data.Values) > 0 {
			targetPage = parseVersionPageIndex(data.Values[0])
		} else {
			targetPage = 0
		}
	case customID == customIDVersionNoop:
		return true
	default:
		return false
	}

	targetPage = clampVersionPage(targetPage)
	embed := BuildVersionPageEmbed(targetPage)
	components := BuildVersionComponents(targetPage)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
	return true
}

// BuildVersionEmbed constructs the version and updates embed for the latest release (page 0).
func BuildVersionEmbed() *discordgo.MessageEmbed {
	return BuildVersionPageEmbed(0)
}

// BuildVersionPageEmbed constructs the version and release notes embed for the given release page index.
func BuildVersionPageEmbed(page int) *discordgo.MessageEmbed {
	totalPages := len(version.History)
	if totalPages == 0 {
		return &discordgo.MessageEmbed{
			Title:       "🚀 Syn Discord Bot • System Version",
			Description: "Production Discord bot designed for server management, 18+ verification, reaction panels, and ticket support.",
			Color:       0x5865F2,
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
					Name:   "✨ Release Highlights",
					Value:  "• Performance and stability enhancements\n",
					Inline: false,
				},
			},
			Footer: &discordgo.MessageEmbedFooter{
				Text: "Syn Support Suite • Ephemeral • Only you can see this message",
			},
		}
	}

	page = clampVersionPage(page)
	rel := version.History[page]

	var changesList strings.Builder
	for _, c := range rel.Changes {
		changesList.WriteString(fmt.Sprintf("• %s\n", c))
	}

	releaseFieldTitle := fmt.Sprintf("📜 Release Notes — %s (%s)", rel.Version, rel.Date)
	if page == 0 {
		releaseFieldTitle = fmt.Sprintf("✨ Latest Release Highlights — %s (%s)", rel.Version, rel.Date)
	}

	return &discordgo.MessageEmbed{
		Title:       "🚀 Syn Discord Bot • System Version & Changelog",
		Description: "Production Discord bot designed for server management, 18+ verification, reaction panels, and ticket support.",
		Color:       0x5865F2, // Discord Blurple
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Current Bot Version",
				Value:  fmt.Sprintf("`%s` (%s)", version.CurrentVersion, version.ReleaseDate),
				Inline: true,
			},
			{
				Name:   "Source Code Repository",
				Value:  fmt.Sprintf("[GitHub: nikorasukyasuta/Syn](%s)", version.GitRepositoryURL),
				Inline: true,
			},
			{
				Name:   releaseFieldTitle,
				Value:  changesList.String(),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Release %d of %d (%s) • Ephemeral • Only you can see this message", page+1, totalPages, rel.Version),
		},
	}
}

// BuildVersionComponents constructs the pagination buttons and version-jump select menu for the given page.
func BuildVersionComponents(page int) []discordgo.MessageComponent {
	totalPages := len(version.History)
	if totalPages <= 1 {
		return nil
	}

	page = clampVersionPage(page)
	prevPage := page - 1
	if prevPage < 0 {
		prevPage = 0
	}
	nextPage := page + 1
	if nextPage >= totalPages {
		nextPage = totalPages - 1
	}

	navRow := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				CustomID: fmt.Sprintf("%s0", customIDVersionFirst),
				Label:    "Latest",
				Style:    discordgo.SecondaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "⏮️"},
				Disabled: page == 0,
			},
			discordgo.Button{
				CustomID: fmt.Sprintf("%s%d", customIDVersionPrev, prevPage),
				Label:    "Newer",
				Style:    discordgo.PrimaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "◀️"},
				Disabled: page == 0,
			},
			discordgo.Button{
				CustomID: customIDVersionNoop,
				Label:    fmt.Sprintf("Page %d / %d (%s)", page+1, totalPages, version.History[page].Version),
				Style:    discordgo.SecondaryButton,
				Disabled: true,
			},
			discordgo.Button{
				CustomID: fmt.Sprintf("%s%d", customIDVersionNext, nextPage),
				Label:    "Older",
				Style:    discordgo.PrimaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "▶️"},
				Disabled: page >= totalPages-1,
			},
			discordgo.Button{
				CustomID: fmt.Sprintf("%s%d", customIDVersionLast, totalPages-1),
				Label:    "Oldest",
				Style:    discordgo.SecondaryButton,
				Emoji:    &discordgo.ComponentEmoji{Name: "⏭️"},
				Disabled: page >= totalPages-1,
			},
		},
	}

	var selectOpts []discordgo.SelectMenuOption
	for idx, rel := range version.History {
		label := fmt.Sprintf("%s (%s)", rel.Version, rel.Date)
		if idx == 0 {
			label += " — Latest"
		}
		desc := fmt.Sprintf("%d update(s)", len(rel.Changes))
		if len(rel.Changes) > 0 {
			first := rel.Changes[0]
			if len(first) > 95 {
				first = first[:92] + "..."
			}
			desc = first
		}
		selectOpts = append(selectOpts, discordgo.SelectMenuOption{
			Label:       label,
			Value:       strconv.Itoa(idx),
			Description: desc,
			Default:     idx == page,
		})
	}

	selectRow := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    customIDVersionSelect,
				Placeholder: "Jump to a specific version release...",
				Options:     selectOpts,
			},
		},
	}

	return []discordgo.MessageComponent{navRow, selectRow}
}

func parseVersionPageIndex(raw string) int {
	val, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return val
}

func clampVersionPage(page int) int {
	total := len(version.History)
	if total == 0 || page < 0 {
		return 0
	}
	if page >= total {
		return total - 1
	}
	return page
}

