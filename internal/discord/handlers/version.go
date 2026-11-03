package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
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

// VersionHandler handles the /version slash command, its interactive pagination controls,
// and posting bot update announcements to the server's announcement channel.
type VersionHandler struct {
	cfg    *config.Config
	logger *slog.Logger
}

// NewVersionHandler creates a new VersionHandler.
func NewVersionHandler(opts ...func(*VersionHandler)) *VersionHandler {
	h := &VersionHandler{}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

// WithVersionConfig attaches runtime Config and Logger to VersionHandler.
func WithVersionConfig(cfg *config.Config, logger *slog.Logger) func(*VersionHandler) {
	return func(h *VersionHandler) {
		h.cfg = cfg
		h.logger = logger
	}
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

// AnnounceUpdateCommandDefinition returns the Discord slash command schema for /announce-update (Staff/Admin).
func AnnounceUpdateCommandDefinition() *discordgo.ApplicationCommand {
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:         "announce-update",
		Description:  "[Staff] Post an embedded announcement in the announcement channel that the bot was recently updated",
		DMPermission: &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "channel",
				Description: "Optional channel to post the announcement in (defaults to the server announcement channel)",
				Type:        discordgo.ApplicationCommandOptionChannel,
				Required:    false,
				ChannelTypes: []discordgo.ChannelType{
					discordgo.ChannelTypeGuildText,
					discordgo.ChannelTypeGuildNews,
				},
			},
		},
	}
}

// HandleAnnounceUpdateCommand posts the embedded bot update announcement to the announcement channel.
func (h *VersionHandler) HandleAnnounceUpdateCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := i.GuildID
	var configuredChID string
	if h.cfg != nil {
		if guildID == "" {
			guildID = h.cfg.GuildID
		}
		configuredChID = h.cfg.AnnouncementChannelID
	}

	targetChannelID := ""
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "channel" {
			if ch := opt.ChannelValue(s); ch != nil && ch.ID != "" {
				targetChannelID = ch.ID
			}
		}
	}

	if targetChannelID == "" {
		resolvedID, err := ResolveAnnouncementChannelID(s, guildID, configuredChID)
		if err != nil {
			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: fmt.Sprintf("❌ Could not locate an announcement channel: %v", err),
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			})
			return
		}
		targetChannelID = resolvedID
	}

	embed := BuildUpdateAnnouncementEmbed()
	sentMsg, err := s.ChannelMessageSendEmbed(targetChannelID, embed)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("❌ Failed to post update announcement to <#%s>: %v", targetChannelID, err),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if h.logger != nil {
		h.logger.Info("manually posted bot update announcement",
			"channel_id", targetChannelID,
			"message_id", sentMsg.ID,
			"version", version.CurrentVersion,
		)
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ Posted the **%s** bot update announcement in <#%s>.", version.CurrentVersion, targetChannelID),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// BuildUpdateAnnouncementEmbed constructs a rich Discord embed announcing that the bot has been recently updated.
func BuildUpdateAnnouncementEmbed() *discordgo.MessageEmbed {
	rel := version.Release{
		Version: version.CurrentVersion,
		Date:    version.ReleaseDate,
	}
	if len(version.History) > 0 {
		rel = version.History[0]
	}

	var changesList strings.Builder
	for _, c := range rel.Changes {
		changesList.WriteString(fmt.Sprintf("• %s\n", c))
	}
	if changesList.Len() == 0 {
		changesList.WriteString("• General stability, security, and feature improvements.\n")
	}

	return &discordgo.MessageEmbed{
		Title: fmt.Sprintf("📢 Syn Bot Update Announcement • %s", rel.Version),
		Description: fmt.Sprintf(
			"**Syn** has been recently updated to **%s** (`%s`)! 🚀\n\n"+
				"We've rolled out new privacy features, verification improvements, and server enhancements. Check out what's new below:",
			rel.Version,
			rel.Date,
		),
		Color: 0x5865F2,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   fmt.Sprintf("✨ What's New in %s", rel.Version),
				Value:  changesList.String(),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Syn Discord Bot • Version %s • Released %s", rel.Version, rel.Date),
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

// EnsureUpdateAnnouncement locates the server's announcement channel and posts an embedded announcement
// that the bot has been recently updated if an announcement for the current version has not already been posted.
func (h *VersionHandler) EnsureUpdateAnnouncement(s *discordgo.Session, guildID string) (string, bool, error) {
	var configuredChID string
	if h.cfg != nil {
		if guildID == "" {
			guildID = h.cfg.GuildID
		}
		configuredChID = h.cfg.AnnouncementChannelID
	}

	channelID, err := ResolveAnnouncementChannelID(s, guildID, configuredChID)
	if err != nil {
		return "", false, err
	}

	// Check recent messages in the announcement channel so container restarts do not post duplicate announcements for the same version.
	if msgs, err := s.ChannelMessages(channelID, 25, "", "", ""); err == nil {
		for _, m := range msgs {
			if m == nil {
				continue
			}
			for _, emb := range m.Embeds {
				if emb == nil {
					continue
				}
				if strings.Contains(emb.Title, version.CurrentVersion) ||
					(emb.Footer != nil && strings.Contains(emb.Footer.Text, "Version "+version.CurrentVersion)) {
					return channelID, false, nil
				}
			}
		}
	}

	embed := BuildUpdateAnnouncementEmbed()
	if _, err := s.ChannelMessageSendEmbed(channelID, embed); err != nil {
		return channelID, false, fmt.Errorf("sending update announcement embed: %w", err)
	}

	return channelID, true, nil
}

// ResolveAnnouncementChannelID finds the target announcement channel in the guild.
// It checks configuredChannelID first, then searches guild channels by name ("announcements", "announcement", etc.)
// or Discord's native GuildNews announcement channel type.
func ResolveAnnouncementChannelID(s *discordgo.Session, guildID, configuredChannelID string) (string, error) {
	if trimmed := strings.TrimSpace(configuredChannelID); trimmed != "" {
		return trimmed, nil
	}
	if s == nil || strings.TrimSpace(guildID) == "" {
		return "", errors.New("guild ID is required to resolve announcement channel")
	}

	channels, err := s.GuildChannels(guildID)
	if err != nil {
		return "", fmt.Errorf("fetching guild channels: %w", err)
	}

	bestID := ""
	bestScore := 0

	for _, ch := range channels {
		if ch == nil {
			continue
		}
		if ch.Type != discordgo.ChannelTypeGuildText && ch.Type != discordgo.ChannelTypeGuildNews {
			continue
		}

		score := scoreAnnouncementChannel(ch)
		if score > bestScore {
			bestScore = score
			bestID = ch.ID
		}
	}

	if bestID == "" {
		return "", errors.New("no announcement channel found in server")
	}
	return bestID, nil
}

func scoreAnnouncementChannel(ch *discordgo.Channel) int {
	clean := cleanChannelName(ch.Name)
	switch clean {
	case "announcements", "announcement":
		return 100
	case "server-announcements", "bot-announcements", "bot-updates", "updates":
		return 85
	}
	if strings.Contains(clean, "announcement") {
		return 70
	}
	if ch.Type == discordgo.ChannelTypeGuildNews {
		return 50
	}
	return 0
}

func cleanChannelName(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	trimmed := strings.TrimFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	return trimmed
}
