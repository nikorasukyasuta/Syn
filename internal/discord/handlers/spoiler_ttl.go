package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/domain"
	"syn/internal/service"
	"sync"
)

const (
	// CustomIDs for the ephemeral Spoilered Photo TTL prompt components.
	SelectSpoilerTTLDaysCustomID    = "select_spoiler_ttl_days"
	SelectSpoilerTTLHoursCustomID   = "select_spoiler_ttl_hours"
	SelectSpoilerTTLMinutesCustomID = "select_spoiler_ttl_minutes"
	BtnSpoilerTTLConfirmCustomID    = "btn_spoiler_ttl_confirm"
	BtnSpoilerTTLDisableCustomID    = "btn_spoiler_ttl_disable"
)

// spoilerTTLDraft holds a user's in-progress dropdown selections before confirming.
type spoilerTTLDraft struct {
	Days    int
	Hours   int
	Minutes int
}

func (d spoilerTTLDraft) Duration() time.Duration {
	return time.Duration(d.Days)*24*time.Hour +
		time.Duration(d.Hours)*time.Hour +
		time.Duration(d.Minutes)*time.Minute
}

// SpoilerTTLHandler handles the /spoiler-ttl slash command, ephemeral dropdown/button interactions,
// and automatic TTL scheduling for spoilered photos uploaded by users across the server.
type SpoilerTTLHandler struct {
	svc    *service.SpoilerTTLService
	cfg    *config.Config
	logger *slog.Logger

	mu     sync.RWMutex
	drafts map[string]spoilerTTLDraft // "guildID:userID" -> draft
}

// NewSpoilerTTLHandler constructs a new SpoilerTTLHandler.
func NewSpoilerTTLHandler(
	svc *service.SpoilerTTLService,
	cfg *config.Config,
	logger *slog.Logger,
) *SpoilerTTLHandler {
	return &SpoilerTTLHandler{
		svc:    svc,
		cfg:    cfg,
		logger: logger,
		drafts: make(map[string]spoilerTTLDraft),
	}
}

// SpoilerTTLCommandDefinition returns the Discord slash command schema for /spoiler-ttl.
// Available to all server members.
func SpoilerTTLCommandDefinition() *discordgo.ApplicationCommand {
	dmPermission := false
	minValZero := 0.0

	return &discordgo.ApplicationCommand{
		Name:         "spoiler-ttl",
		Description:  "Set a personal auto-delete timer (30m to 14d) for all spoilered NSFW photos you post",
		DMPermission: &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "days",
				Description: "Optional initial days selection (0 - 14)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
				MinValue:    &minValZero,
				MaxValue:    14,
			},
			{
				Name:        "hours",
				Description: "Optional initial hours selection (0 - 23)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
				MinValue:    &minValZero,
				MaxValue:    23,
			},
			{
				Name:        "minutes",
				Description: "Optional initial minutes selection (0 - 59)",
				Type:        discordgo.ApplicationCommandOptionInteger,
				Required:    false,
				MinValue:    &minValZero,
				MaxValue:    59,
			},
		},
	}
}

func draftKey(guildID, userID string) string {
	return guildID + ":" + userID
}

// HandleSpoilerTTLCommand opens an ephemeral prompt with dropdown menus for Days, Hours, and Minutes
// and a green confirmation button so the user can configure their spoilered photo TTL.
func (h *SpoilerTTLHandler) HandleSpoilerTTLCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := interactionUserID(i)
	if userID == "" {
		return
	}

	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	// Default initial draft to the user's current active setting, or 30 minutes if none is set.
	draft := spoilerTTLDraft{Days: 0, Hours: 0, Minutes: 30}
	if currentTTL, ok := h.svc.GetUserTTL(guildID, userID); ok && currentTTL > 0 {
		d, hr, m := service.SplitSpoilerTTL(currentTTL)
		draft = spoilerTTLDraft{Days: d, Hours: hr, Minutes: m}
	}

	// If the user passed optional days/hours/minutes on the command, pre-populate the draft with those.
	opts := i.ApplicationCommandData().Options
	if len(opts) > 0 {
		customDraft := spoilerTTLDraft{}
		for _, opt := range opts {
			switch opt.Name {
			case "days":
				customDraft.Days = int(opt.IntValue())
			case "hours":
				customDraft.Hours = int(opt.IntValue())
			case "minutes":
				customDraft.Minutes = int(opt.IntValue())
			}
		}
		draft = customDraft
	}

	h.setDraft(guildID, userID, draft)

	embed, components := h.buildPromptView(guildID, userID, draft, "")
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

// HandleComponentInteraction processes dropdown selections and button clicks for the Spoilered Photo TTL prompt.
// Returns true if the interaction custom_id belongs to SpoilerTTLHandler.
func (h *SpoilerTTLHandler) HandleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i == nil || i.Type != discordgo.InteractionMessageComponent {
		return false
	}

	customID := i.MessageComponentData().CustomID
	switch customID {
	case SelectSpoilerTTLDaysCustomID,
		SelectSpoilerTTLHoursCustomID,
		SelectSpoilerTTLMinutesCustomID:
		h.handleDropdownChange(s, i, customID)
		return true
	case BtnSpoilerTTLConfirmCustomID:
		h.handleConfirmButton(s, i)
		return true
	case BtnSpoilerTTLDisableCustomID:
		h.handleDisableButton(s, i)
		return true
	default:
		return false
	}
}

func (h *SpoilerTTLHandler) handleDropdownChange(s *discordgo.Session, i *discordgo.InteractionCreate, customID string) {
	userID := interactionUserID(i)
	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	parsedVal, err := strconv.Atoi(values[0])
	if err != nil {
		h.respondEphemeralError(s, i, "Invalid numeric selection.")
		return
	}

	draft := h.getDraft(guildID, userID)
	switch customID {
	case SelectSpoilerTTLDaysCustomID:
		draft.Days = parsedVal
	case SelectSpoilerTTLHoursCustomID:
		draft.Hours = parsedVal
	case SelectSpoilerTTLMinutesCustomID:
		draft.Minutes = parsedVal
	}
	h.setDraft(guildID, userID, draft)

	var notice string
	if err := service.ValidateSpoilerTTL(draft.Duration()); err != nil {
		notice = "⚠️ " + err.Error() + ". Please adjust the dropdowns before confirming."
	}

	embed, components := h.buildPromptView(guildID, userID, draft, notice)
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *SpoilerTTLHandler) handleConfirmButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := interactionUserID(i)
	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	draft := h.getDraft(guildID, userID)
	selectedDuration := draft.Duration()

	if err := service.ValidateSpoilerTTL(selectedDuration); err != nil {
		notice := "❌ Cannot confirm: " + err.Error() + ". Minimum is **30 minutes** and maximum is **14 days**."
		embed, components := h.buildPromptView(guildID, userID, draft, notice)
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Embeds:     []*discordgo.MessageEmbed{embed},
				Components: components,
				Flags:      discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.svc.SetUserTTL(ctx, guildID, userID, selectedDuration); err != nil {
		h.respondEphemeralError(s, i, fmt.Sprintf("Failed to save your spoilered photo time limit: %v", err))
		return
	}

	confirmMsg := fmt.Sprintf("✅ **Confirmed!** All spoilered photos you post will automatically be deleted after **%s**.", service.FormatSpoilerTTL(selectedDuration))
	embed, components := h.buildPromptView(guildID, userID, draft, confirmMsg)
	embed.Color = 0x22C55E // Green confirmation color

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *SpoilerTTLHandler) handleDisableButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := interactionUserID(i)
	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.svc.DisableUserTTL(ctx, guildID, userID); err != nil {
		h.respondEphemeralError(s, i, "Failed to disable your spoilered photo auto-deletion timer.")
		return
	}

	draft := h.getDraft(guildID, userID)
	embed, components := h.buildPromptView(guildID, userID, draft, "🛑 Auto-deletion for your spoilered photos has been **disabled**.")
	embed.Color = 0xF59E0B

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

func (h *SpoilerTTLHandler) buildPromptView(
	guildID, userID string,
	draft spoilerTTLDraft,
	statusNotice string,
) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	activeStatus := "Not Configured (Spoilered photos stay until manually deleted)"
	if activeTTL, ok := h.svc.GetUserTTL(guildID, userID); ok && activeTTL > 0 {
		activeStatus = fmt.Sprintf("✅ Active — **%s**", service.FormatSpoilerTTL(activeTTL))
	}

	selectedDur := draft.Duration()
	selectedSummary := service.FormatSpoilerTTL(selectedDur)
	validationErr := service.ValidateSpoilerTTL(selectedDur)
	if validationErr != nil {
		selectedSummary = fmt.Sprintf("%s *(Out of range: must be 30m – 14d)*", selectedSummary)
	}

	desc := "Choose how long your **spoilered NSFW photos** remain on the server before being automatically deleted.\n\n" +
		"• **Minimum Time Limit:** `30 minutes`\n" +
		"• **Maximum Time Limit:** `14 days`\n\n" +
		"Select **Days**, **Hours**, and **Minutes** from the dropdown menus below, then click the green **Confirm Spoilered Photo Time Limit** button."
	if statusNotice != "" {
		desc += "\n\n" + statusNotice
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🕒 Spoilered Photo Auto-Delete Timer (TTL)",
		Description: desc,
		Color:       0x5865F2,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Selected Time Limit (Draft)",
				Value:  selectedSummary,
				Inline: true,
			},
			{
				Name:   "Current Saved Setting",
				Value:  activeStatus,
				Inline: true,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "Applies automatically to all spoilered photos you upload across the server",
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// Build Days options (0..14 -> 15 options)
	dayOptions := make([]discordgo.SelectMenuOption, 0, 15)
	for d := 0; d <= 14; d++ {
		label := fmt.Sprintf("%d days", d)
		if d == 1 {
			label = "1 day"
		}
		dayOptions = append(dayOptions, discordgo.SelectMenuOption{
			Label:   label,
			Value:   strconv.Itoa(d),
			Default: d == draft.Days,
		})
	}

	// Build Hours options (0..23 -> 24 options)
	hourOptions := make([]discordgo.SelectMenuOption, 0, 24)
	for hr := 0; hr <= 23; hr++ {
		label := fmt.Sprintf("%d hours", hr)
		if hr == 1 {
			label = "1 hour"
		}
		hourOptions = append(hourOptions, discordgo.SelectMenuOption{
			Label:   label,
			Value:   strconv.Itoa(hr),
			Default: hr == draft.Hours,
		})
	}

	// Build Minutes options (up to 25 options per Discord SelectMenu constraint; dynamically includes draft.Minutes)
	minuteValues := []int{0, 1, 2, 3, 4, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55}
	hasDraftMin := false
	for _, m := range minuteValues {
		if m == draft.Minutes {
			hasDraftMin = true
			break
		}
	}
	if !hasDraftMin && draft.Minutes >= 0 && draft.Minutes <= 59 {
		minuteValues = append(minuteValues, draft.Minutes)
		sort.Ints(minuteValues)
	}

	minuteOptions := make([]discordgo.SelectMenuOption, 0, len(minuteValues))
	for _, m := range minuteValues {
		label := fmt.Sprintf("%d minutes", m)
		if m == 1 {
			label = "1 minute"
		}
		minuteOptions = append(minuteOptions, discordgo.SelectMenuOption{
			Label:   label,
			Value:   strconv.Itoa(m),
			Default: m == draft.Minutes,
		})
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    SelectSpoilerTTLDaysCustomID,
					Placeholder: "Select Days (0 - 14 days)",
					Options:     dayOptions,
				},
			},
		},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    SelectSpoilerTTLHoursCustomID,
					Placeholder: "Select Hours (0 - 23 hours)",
					Options:     hourOptions,
				},
			},
		},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    SelectSpoilerTTLMinutesCustomID,
					Placeholder: "Select Minutes (0 - 59 minutes)",
					Options:     minuteOptions,
				},
			},
		},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Confirm Spoilered Photo Time Limit",
					Style:    discordgo.SuccessButton,
					CustomID: BtnSpoilerTTLConfirmCustomID,
					Emoji: &discordgo.ComponentEmoji{
						Name: "✅",
					},
				},
				discordgo.Button{
					Label:    "Disable Auto-Delete",
					Style:    discordgo.SecondaryButton,
					CustomID: BtnSpoilerTTLDisableCustomID,
				},
			},
		},
	}

	return embed, components
}

// HandleMessageCreate monitors messages across the server and schedules auto-deletion
// whenever a user with a configured Spoilered Photo TTL posts a spoilered photo.
func (h *SpoilerTTLHandler) HandleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil || m.Author.Bot {
		return
	}

	if h.cfg != nil && h.cfg.GuildID != "" && m.GuildID != "" && m.GuildID != h.cfg.GuildID {
		return
	}

	// Skip Flash and Dash channels since they have their own dedicated 30-minute timer.
	if h.svc.IsFlashDashChannel(m.ChannelID) {
		return
	}

	if !HasSpoilerPhotoAttachment(m.Attachments) {
		return
	}

	guildID := m.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	ttl, ok := h.svc.GetUserTTL(guildID, m.Author.ID)
	if !ok || ttl < domain.MinSpoilerPhotoTTL {
		return
	}

	h.svc.ScheduleSpoilerMessageDeletion(s, guildID, m.ChannelID, m.ID, m.Author.ID, ttl)
}

// IsSpoilerPhotoAttachment checks whether a Discord message attachment is a spoilered photo/image.
func IsSpoilerPhotoAttachment(att *discordgo.MessageAttachment) bool {
	if !IsPhotoAttachment(att) {
		return false
	}
	return strings.HasPrefix(strings.ToUpper(att.Filename), "SPOILER_")
}

// HasSpoilerPhotoAttachment returns true if the message contains at least one spoilered photo attachment.
func HasSpoilerPhotoAttachment(attachments []*discordgo.MessageAttachment) bool {
	for _, att := range attachments {
		if IsSpoilerPhotoAttachment(att) {
			return true
		}
	}
	return false
}

func (h *SpoilerTTLHandler) getDraft(guildID, userID string) spoilerTTLDraft {
	key := draftKey(guildID, userID)
	h.mu.RLock()
	d, ok := h.drafts[key]
	h.mu.RUnlock()
	if ok {
		return d
	}

	if currentTTL, hasSaved := h.svc.GetUserTTL(guildID, userID); hasSaved && currentTTL > 0 {
		days, hours, mins := service.SplitSpoilerTTL(currentTTL)
		return spoilerTTLDraft{Days: days, Hours: hours, Minutes: mins}
	}

	return spoilerTTLDraft{Days: 0, Hours: 0, Minutes: 30}
}

func (h *SpoilerTTLHandler) setDraft(guildID, userID string, draft spoilerTTLDraft) {
	h.mu.Lock()
	h.drafts[draftKey(guildID, userID)] = draft
	h.mu.Unlock()
}

func (h *SpoilerTTLHandler) respondEphemeralError(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "❌ " + msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}
