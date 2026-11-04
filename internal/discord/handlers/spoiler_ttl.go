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

// PrivacyModeCommandName is the Discord slash command name for configuring personal spoilered photo TTLs.
const PrivacyModeCommandName = "privacy-mode"

// SpoilerTTLCommandDefinition returns the Discord slash command schema for /privacy-mode.
// Available to all server members (30m–14d for members, 1m–14d for Server Administrators for debugging).
func SpoilerTTLCommandDefinition() *discordgo.ApplicationCommand {
	dmPermission := false
	minValZero := 0.0

	return &discordgo.ApplicationCommand{
		Name:         PrivacyModeCommandName,
		Description:  "Set a personal auto-delete timer for all spoilered NSFW photos you post",
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

	isAdmin := h.isCallerAdmin(s, i)
	h.setDraft(guildID, userID, draft)

	var statusNotice string
	if len(opts) > 0 {
		selectedDur := draft.Duration()
		if err := service.ValidateSpoilerTTLForUser(selectedDur, isAdmin); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if saveErr := h.svc.SetUserTTLWithAdmin(ctx, guildID, userID, selectedDur, isAdmin); saveErr == nil {
				statusNotice = fmt.Sprintf("✅ **Saved!** All spoilered photos you post will automatically be deleted after **%s**.", service.FormatSpoilerTTL(selectedDur))
				go h.scheduleRecentUserSpoilerMessages(s, guildID, i.ChannelID, userID, selectedDur)
			}
			cancel()
		} else {
			statusNotice = "⚠️ " + err.Error() + ". Please adjust the dropdowns before confirming."
		}
	}

	embed, components := h.buildPromptView(guildID, userID, draft, isAdmin, statusNotice)
	if len(opts) > 0 && strings.HasPrefix(statusNotice, "✅") {
		embed.Color = 0x22C55E
	}
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

	isAdmin := h.isCallerAdmin(s, i)
	var notice string
	selectedDur := draft.Duration()
	if err := service.ValidateSpoilerTTLForUser(selectedDur, isAdmin); err != nil {
		notice = "⚠️ " + err.Error() + ". Please adjust the dropdowns before confirming."
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if saveErr := h.svc.SetUserTTLWithAdmin(ctx, guildID, userID, selectedDur, isAdmin); saveErr == nil {
			notice = fmt.Sprintf("✅ **Saved!** Your spoilered photo auto-delete timer is now set to **%s** (or click Confirm below).", service.FormatSpoilerTTL(selectedDur))
			go h.scheduleRecentUserSpoilerMessages(s, guildID, i.ChannelID, userID, selectedDur)
		}
		cancel()
	}

	embed, components := h.buildPromptView(guildID, userID, draft, isAdmin, notice)
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

	isAdmin := h.isCallerAdmin(s, i)
	draft := h.getDraft(guildID, userID)
	selectedDuration := draft.Duration()

	if err := service.ValidateSpoilerTTLForUser(selectedDuration, isAdmin); err != nil {
		minLabel := "30 minutes"
		if isAdmin {
			minLabel = "1 minute"
		}
		notice := fmt.Sprintf("❌ Cannot confirm: %s. Minimum is **%s** and maximum is **14 days**.", err.Error(), minLabel)
		embed, components := h.buildPromptView(guildID, userID, draft, isAdmin, notice)
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

	if err := h.svc.SetUserTTLWithAdmin(ctx, guildID, userID, selectedDuration, isAdmin); err != nil {
		h.respondEphemeralError(s, i, fmt.Sprintf("Failed to save your spoilered photo time limit: %v", err))
		return
	}

	go h.scheduleRecentUserSpoilerMessages(s, guildID, i.ChannelID, userID, selectedDuration)

	confirmMsg := fmt.Sprintf("✅ **Confirmed!** All spoilered photos you post will automatically be deleted after **%s**.", service.FormatSpoilerTTL(selectedDuration))
	embed, components := h.buildPromptView(guildID, userID, draft, isAdmin, confirmMsg)
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

	isAdmin := h.isCallerAdmin(s, i)
	draft := h.getDraft(guildID, userID)
	embed, components := h.buildPromptView(guildID, userID, draft, isAdmin, "🛑 Auto-deletion for your spoilered photos has been **disabled**.")
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
	isAdmin bool,
	statusNotice string,
) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	activeStatus := "Not Configured (Spoilered photos stay until manually deleted)"
	if activeTTL, ok := h.svc.GetUserTTL(guildID, userID); ok && activeTTL > 0 {
		activeStatus = fmt.Sprintf("✅ Active — **%s**", service.FormatSpoilerTTL(activeTTL))
	}

	selectedDur := draft.Duration()
	selectedSummary := service.FormatSpoilerTTL(selectedDur)
	validationErr := service.ValidateSpoilerTTLForUser(selectedDur, isAdmin)
	minShortLabel := "30m"
	minDisplayLabel := "`30 minutes`"
	if isAdmin {
		minShortLabel = "1m"
		minDisplayLabel = "`1 minute` *(Admin override; 30 minutes for members)*"
	}
	if validationErr != nil {
		selectedSummary = fmt.Sprintf("%s *(Out of range: must be %s – 14d)*", selectedSummary, minShortLabel)
	}

	desc := "Choose how long your **spoilered NSFW photos** remain on the server before being automatically deleted.\n\n" +
		"• **Minimum Time Limit:** " + minDisplayLabel + "\n" +
		"• **Maximum Time Limit:** `14 days`\n\n" +
		"Select **Days**, **Hours**, and **Minutes** from the dropdown menus below, then click the green **Confirm Spoilered Photo Time Limit** button."
	if statusNotice != "" {
		desc += "\n\n" + statusNotice
	}

	embed := &discordgo.MessageEmbed{
		Title:       "🔒 Privacy Mode • Spoilered Photo Auto-Delete Timer",
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

	if !HasSpoilerPhotoMessage(m.Message) {
		return
	}

	guildID := m.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}

	ttl, ok := h.svc.GetUserTTL(guildID, m.Author.ID)
	if !ok || ttl < domain.MinAdminSpoilerPhotoTTL {
		return
	}

	// If this is a Flash and Dash channel, only skip if the channel's Flash and Dash timer
	// is already shorter than or equal to the user's personal privacy-mode TTL.
	if flashDur, isFlash := h.svc.GetFlashDashChannelDuration(m.ChannelID); isFlash && flashDur > 0 && ttl >= flashDur {
		return
	}

	h.svc.ScheduleSpoilerMessageDeletion(s, guildID, m.ChannelID, m.ID, m.Author.ID, ttl)
}

// scheduleRecentUserSpoilerMessages scans the last 25 messages in channelID and schedules any
// spoilered photos authored by userID so that photos posted right before confirming /privacy-mode
// are also automatically deleted.
func (h *SpoilerTTLHandler) scheduleRecentUserSpoilerMessages(
	s *discordgo.Session,
	guildID, channelID, userID string,
	d time.Duration,
) {
	if s == nil || channelID == "" || userID == "" || d < domain.MinAdminSpoilerPhotoTTL {
		return
	}

	msgs, err := s.ChannelMessages(channelID, 25, "", "", "")
	if err != nil {
		return
	}

	now := time.Now().UTC()
	for _, msg := range msgs {
		if msg == nil || msg.Author == nil || msg.Author.ID != userID {
			continue
		}
		if !HasSpoilerPhotoMessage(msg) {
			continue
		}
		remaining := d
		if !msg.Timestamp.IsZero() && msg.Timestamp.Before(now) {
			elapsed := now.Sub(msg.Timestamp.UTC())
			// Only retroactively schedule recent messages posted within the last 14 days
			if elapsed > domain.MaxSpoilerPhotoTTL {
				continue
			}
			if elapsed < d {
				remaining = d - elapsed
			} else if elapsed <= 15*time.Minute {
				remaining = 3 * time.Second
			}
		}
		h.svc.ScheduleSpoilerMessageDeletion(s, guildID, channelID, msg.ID, userID, remaining)
	}
}

func (h *SpoilerTTLHandler) isCallerAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	if (i.Member.Permissions & (discordgo.PermissionAdministrator | discordgo.PermissionManageGuild)) != 0 {
		return true
	}
	if h.cfg != nil {
		for _, staffRoleID := range h.cfg.StaffRoleIDs {
			for _, mr := range i.Member.Roles {
				if mr != "" && mr == staffRoleID {
					return true
				}
			}
		}
	}
	guildID := i.GuildID
	if guildID == "" && h.cfg != nil {
		guildID = h.cfg.GuildID
	}
	if s != nil && s.State != nil && guildID != "" {
		if g, err := s.State.Guild(guildID); err == nil && g != nil {
			if i.Member.User != nil && g.OwnerID != "" && g.OwnerID == i.Member.User.ID {
				return true
			}
			for _, r := range g.Roles {
				if r != nil && (r.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageGuild)) != 0 {
					for _, mr := range i.Member.Roles {
						if mr == r.ID {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// attachmentFlagIsSpoiler is Discord's API v10 attachment bitflag (1 << 3) indicating a spoilered attachment.
const attachmentFlagIsSpoiler discordgo.MessageAttachmentFlags = 1 << 3

// IsSpoilerPhotoAttachment checks whether a Discord message attachment is a spoilered photo/image/media file.
func IsSpoilerPhotoAttachment(att *discordgo.MessageAttachment) bool {
	if att == nil {
		return false
	}
	isMedia := IsPhotoAttachment(att) || (att.Width > 0 && att.Height > 0) ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(att.ContentType)), "video/")
	if !isMedia {
		return false
	}
	if (att.Flags & attachmentFlagIsSpoiler) != 0 {
		return true
	}
	upperName := strings.ToUpper(strings.TrimSpace(att.Filename))
	if strings.HasPrefix(upperName, "SPOILER_") || strings.Contains(upperName, "SPOILER_") {
		return true
	}
	if strings.Contains(strings.ToUpper(att.URL), "/SPOILER_") || strings.Contains(strings.ToUpper(att.ProxyURL), "/SPOILER_") {
		return true
	}
	return false
}

// HasSpoilerPhotoAttachment returns true if the attachment slice contains at least one spoilered photo attachment.
func HasSpoilerPhotoAttachment(attachments []*discordgo.MessageAttachment) bool {
	for _, att := range attachments {
		if IsSpoilerPhotoAttachment(att) {
			return true
		}
	}
	return false
}

// HasSpoilerPhotoMessage checks whether a Discord message contains a spoilered photo attachment,
// a forwarded spoilered photo snapshot, or a spoiler-tagged (||...||) image embed/link.
func HasSpoilerPhotoMessage(msg *discordgo.Message) bool {
	if msg == nil {
		return false
	}
	if HasSpoilerPhotoAttachment(msg.Attachments) {
		return true
	}
	for _, snap := range msg.MessageSnapshots {
		if snap.Message != nil && HasSpoilerPhotoAttachment(snap.Message.Attachments) {
			return true
		}
	}
	// Check if message content wraps an image URL or image embed in Discord spoiler bars ||...||
	if strings.Count(msg.Content, "||") >= 2 {
		for _, emb := range msg.Embeds {
			if emb == nil {
				continue
			}
			if emb.Image != nil || emb.Thumbnail != nil || emb.Video != nil ||
				strings.EqualFold(string(emb.Type), "image") || strings.EqualFold(string(emb.Type), "gifv") {
				return true
			}
		}
		lowerContent := strings.ToLower(msg.Content)
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".avif"} {
			if strings.Contains(lowerContent, ext) && strings.Contains(lowerContent, "http") {
				return true
			}
		}
	}
	return false
}

func (h *SpoilerTTLHandler) getDraft(guildID, userID string) spoilerTTLDraft {
	h.mu.RLock()
	d, ok := h.drafts[draftKey(guildID, userID)]
	if !ok {
		d, ok = h.drafts[draftKey("", userID)]
	}
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
	h.drafts[draftKey("", userID)] = draft
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
