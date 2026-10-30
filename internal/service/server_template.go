package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/domain"
)

// ServerTemplateService handles exporting Discord server state (roles, categories, channels,
// and permission overwrites) into a portable JSON template and translating template state
// onto a local development Discord server.
type ServerTemplateService struct {
	logger *slog.Logger
}

// NewServerTemplateService constructs a new ServerTemplateService.
func NewServerTemplateService(logger *slog.Logger) *ServerTemplateService {
	if logger == nil {
		logger = slog.Default()
	}
	return &ServerTemplateService{logger: logger}
}

// BuildTemplate constructs a deterministic ServerTemplate snapshot from live Discord guild state.
func (s *ServerTemplateService) BuildTemplate(
	guild *discordgo.Guild,
	roles []*discordgo.Role,
	channels []*discordgo.Channel,
	cfg *config.Config,
) *domain.ServerTemplate {
	guildID := ""
	guildName := ""
	verLevel := 0
	contentFilter := 0
	defaultNotifs := 0

	if guild != nil {
		guildID = guild.ID
		guildName = guild.Name
		verLevel = int(guild.VerificationLevel)
		contentFilter = int(guild.ExplicitContentFilter)
		defaultNotifs = int(guild.DefaultMessageNotifications)
	} else if cfg != nil {
		guildID = cfg.GuildID
	}

	// Build role lookup by ID so channel permission overwrites can also record Role Name & Color
	roleByID := make(map[string]*discordgo.Role, len(roles))
	var roleTemplates []domain.RoleTemplate

	for _, r := range roles {
		if r == nil {
			continue
		}
		roleByID[r.ID] = r
		// Skip bot-managed integration roles; Discord provisions those per bot integration automatically
		if r.Managed {
			continue
		}
		isEveryone := r.ID == guildID || r.Name == "@everyone"
		roleTemplates = append(roleTemplates, domain.RoleTemplate{
			OriginalID:  r.ID,
			Name:        r.Name,
			Color:       r.Color,
			Hoist:       r.Hoist,
			Permissions: r.Permissions,
			Mentionable: r.Mentionable,
			Position:    r.Position,
			IsEveryone:  isEveryone,
		})
	}

	// Sort roles by Position ascending (lowest to highest) so recreation preserves hierarchy order
	sort.SliceStable(roleTemplates, func(i, j int) bool {
		if roleTemplates[i].IsEveryone != roleTemplates[j].IsEveryone {
			return roleTemplates[i].IsEveryone
		}
		if roleTemplates[i].Position != roleTemplates[j].Position {
			return roleTemplates[i].Position < roleTemplates[j].Position
		}
		return roleTemplates[i].OriginalID < roleTemplates[j].OriginalID
	})

	// Index category names by OriginalID
	categoryNameByID := make(map[string]string)
	var categories []domain.CategoryTemplate
	var regularChannels []domain.ChannelTemplate

	for _, ch := range channels {
		if ch == nil {
			continue
		}
		if ch.Type == discordgo.ChannelTypeGuildCategory {
			categoryNameByID[ch.ID] = ch.Name
			categories = append(categories, domain.CategoryTemplate{
				OriginalID: ch.ID,
				Name:       ch.Name,
				Position:   ch.Position,
				NSFW:       ch.NSFW,
				Overwrites: extractOverwriteTemplates(ch.PermissionOverwrites, guildID, roleByID),
			})
		}
	}

	sort.SliceStable(categories, func(i, j int) bool {
		if categories[i].Position != categories[j].Position {
			return categories[i].Position < categories[j].Position
		}
		return categories[i].OriginalID < categories[j].OriginalID
	})

	for _, ch := range channels {
		if ch == nil || ch.Type == discordgo.ChannelTypeGuildCategory {
			continue
		}
		// Skip ephemeral active ticket or ID verification session channels
		lowerName := strings.ToLower(ch.Name)
		if strings.HasPrefix(lowerName, "id-verify-") ||
			strings.HasPrefix(lowerName, "ticket-general-") ||
			strings.HasPrefix(lowerName, "ticket-report-") ||
			strings.HasPrefix(lowerName, "ticket-warn-") {
			continue
		}

		regularChannels = append(regularChannels, domain.ChannelTemplate{
			OriginalID:         ch.ID,
			Name:               ch.Name,
			Type:               ch.Type,
			Topic:              ch.Topic,
			Position:           ch.Position,
			NSFW:               ch.NSFW,
			RateLimitPerUser:   ch.RateLimitPerUser,
			Bitrate:            ch.Bitrate,
			UserLimit:          ch.UserLimit,
			ParentOriginalID:   ch.ParentID,
			ParentCategoryName: categoryNameByID[ch.ParentID],
			Overwrites:         extractOverwriteTemplates(ch.PermissionOverwrites, guildID, roleByID),
		})
	}

	sort.SliceStable(regularChannels, func(i, j int) bool {
		if regularChannels[i].ParentOriginalID != regularChannels[j].ParentOriginalID {
			return regularChannels[i].ParentOriginalID < regularChannels[j].ParentOriginalID
		}
		if regularChannels[i].Position != regularChannels[j].Position {
			return regularChannels[i].Position < regularChannels[j].Position
		}
		return regularChannels[i].OriginalID < regularChannels[j].OriginalID
	})

	var botMapping domain.BotRoleTemplateMapping
	if cfg != nil {
		botMapping = domain.BotRoleTemplateMapping{
			VerifiedRoleOriginalID:      cfg.VerifiedRoleID,
			IDVerifiedRoleOriginalID:    cfg.IDVerifiedRoleID,
			UnverifiedRoleOriginalID:    cfg.UnverifiedRoleID,
			StaffRoleOriginalIDs:        append([]string(nil), cfg.StaffRoleIDs...),
			TicketCategoryOriginalID:    cfg.TicketCategoryID,
			TicketLogChannelOriginalID:  cfg.TicketLogChannelID,
			StaffAlertChannelOriginalID: cfg.StaffAlertChannelID,
		}
	}

	return &domain.ServerTemplate{
		Version:                     "1.0",
		ExportedAt:                  time.Now().UTC(),
		SourceGuildID:               guildID,
		SourceGuildName:             guildName,
		VerificationLevel:           verLevel,
		ExplicitContentFilter:       contentFilter,
		DefaultMessageNotifications: defaultNotifs,
		Roles:                       roleTemplates,
		Categories:                  categories,
		Channels:                    regularChannels,
		BotMapping:                  botMapping,
	}
}

func extractOverwriteTemplates(
	overwrites []*discordgo.PermissionOverwrite,
	guildID string,
	roleByID map[string]*discordgo.Role,
) []domain.PermissionOverwriteTemplate {
	if len(overwrites) == 0 {
		return nil
	}
	var out []domain.PermissionOverwriteTemplate
	for _, ov := range overwrites {
		if ov == nil {
			continue
		}
		// Only export role permission overwrites (member-specific overwrites are tied to individual users)
		if ov.Type != discordgo.PermissionOverwriteTypeRole {
			continue
		}
		isEveryone := ov.ID == guildID
		roleName := ""
		roleColor := 0
		if r, ok := roleByID[ov.ID]; ok && r != nil {
			roleName = r.Name
			roleColor = r.Color
			if r.Name == "@everyone" {
				isEveryone = true
			}
		}
		out = append(out, domain.PermissionOverwriteTemplate{
			TargetType:       ov.Type,
			TargetOriginalID: ov.ID,
			TargetRoleName:   roleName,
			TargetRoleColor:  roleColor,
			IsEveryone:       isEveryone,
			Allow:            ov.Allow,
			Deny:             ov.Deny,
		})
	}
	return out
}

// SaveTemplate marshals the ServerTemplate to formatted JSON and writes it to filePath.
func (s *ServerTemplateService) SaveTemplate(filePath string, tmpl *domain.ServerTemplate) ([]byte, error) {
	if tmpl == nil {
		return nil, fmt.Errorf("server template cannot be nil")
	}
	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling server template: %w", err)
	}
	if filePath != "" {
		if dir := filepath.Dir(filePath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("creating template directory %s: %w", dir, err)
			}
		}
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return nil, fmt.Errorf("writing server template file %s: %w", filePath, err)
		}
	}
	return data, nil
}

// LoadTemplate reads and unmarshals a ServerTemplate JSON file from disk.
func (s *ServerTemplateService) LoadTemplate(filePath string) (*domain.ServerTemplate, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading server template file %s: %w", filePath, err)
	}
	var tmpl domain.ServerTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		return nil, fmt.Errorf("parsing server template file %s: %w", filePath, err)
	}
	return &tmpl, nil
}

// TranslateOverwrites converts template permission overwrites into Discord API PermissionOverwrite structs
// using the newly provisioned roleIDMap (OriginalRoleID -> DevServerRoleID) and targetGuildID (@everyone).
func (s *ServerTemplateService) TranslateOverwrites(
	overwrites []domain.PermissionOverwriteTemplate,
	targetGuildID string,
	roleIDMap map[string]string,
	targetRoles []*discordgo.Role,
) []*discordgo.PermissionOverwrite {
	if len(overwrites) == 0 {
		return nil
	}

	var result []*discordgo.PermissionOverwrite
	seenTargets := make(map[string]bool)

	for _, ov := range overwrites {
		if ov.TargetType != discordgo.PermissionOverwriteTypeRole {
			continue
		}

		var resolvedRoleID string
		if ov.IsEveryone {
			resolvedRoleID = targetGuildID
		} else if mappedID, ok := roleIDMap[ov.TargetOriginalID]; ok && mappedID != "" {
			resolvedRoleID = mappedID
		} else if ov.TargetRoleName != "" {
			// Fallback: match by exact name + color first, then by name
			for _, r := range targetRoles {
				if r != nil && strings.EqualFold(r.Name, ov.TargetRoleName) && r.Color == ov.TargetRoleColor {
					resolvedRoleID = r.ID
					break
				}
			}
			if resolvedRoleID == "" {
				for _, r := range targetRoles {
					if r != nil && strings.EqualFold(r.Name, ov.TargetRoleName) {
						resolvedRoleID = r.ID
						break
					}
				}
			}
		}

		if resolvedRoleID == "" || seenTargets[resolvedRoleID] {
			continue
		}
		seenTargets[resolvedRoleID] = true

		result = append(result, &discordgo.PermissionOverwrite{
			ID:    resolvedRoleID,
			Type:  discordgo.PermissionOverwriteTypeRole,
			Allow: ov.Allow,
			Deny:  ov.Deny,
		})
	}

	return result
}

// WriteDevDiscordIDsFile generates a development-ready config/discord_ids.dev.json file
// mapping the newly created development server's role and channel IDs.
func (s *ServerTemplateService) WriteDevDiscordIDsFile(
	filePath string,
	devGuildID string,
	mapping domain.BotRoleTemplateMapping,
	roleIDMap map[string]string,
	channelIDMap map[string]string,
	baseCfg *config.Config,
) error {
	var staffIDs []string
	for _, origStaffID := range mapping.StaffRoleOriginalIDs {
		if mapped, ok := roleIDMap[origStaffID]; ok && mapped != "" {
			staffIDs = append(staffIDs, mapped)
		}
	}

	var out config.DiscordIDs
	out.Environment = config.EnvironmentDevelopment
	out.GuildID = devGuildID
	out.Roles.VerifiedRoleID = roleIDMap[mapping.VerifiedRoleOriginalID]
	out.Roles.IDVerifiedRoleID = roleIDMap[mapping.IDVerifiedRoleOriginalID]
	out.Roles.UnverifiedRoleID = roleIDMap[mapping.UnverifiedRoleOriginalID]
	out.Roles.StaffRoleIDs = staffIDs

	out.Channels.TicketCategoryID = channelIDMap[mapping.TicketCategoryOriginalID]
	out.Channels.TicketLogChannelID = channelIDMap[mapping.TicketLogChannelOriginalID]
	out.Channels.StaffAlertChannelID = channelIDMap[mapping.StaffAlertChannelOriginalID]

	out.Settings.Environment = config.EnvironmentDevelopment
	out.Settings.SafewordEmoji = "🛑"
	out.Settings.LogLevel = "DEBUG"
	out.Settings.LogFilePath = "logs/syn.dev.log"
	out.Settings.MetricsLogPath = "logs/metrics.dev.jsonl"
	out.Settings.MetricsSampleIntervalSec = 15
	out.Settings.MetricsFlushIntervalSec = 60
	out.Settings.WebPort = "8080"
	if baseCfg != nil && baseCfg.ServerTemplatePath != "" {
		out.Settings.ServerTemplatePath = baseCfg.ServerTemplatePath
	} else {
		out.Settings.ServerTemplatePath = "config/server_template.json"
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling dev discord_ids json: %w", err)
	}

	if dir := filepath.Dir(filePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}

	return os.WriteFile(filePath, data, 0644)
}

