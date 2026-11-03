package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/domain"
)

// SpoilerTTLService manages per-user auto-deletion TTL timers for spoilered photos
// across the server, modeled on FlashDashService.
type SpoilerTTLService struct {
	repo     domain.SpoilerTTLRepository
	flashSvc *FlashDashService
	logger   *slog.Logger

	mu       sync.RWMutex
	userTTLs map[string]time.Duration // "guildID:userID" -> duration
}

// NewSpoilerTTLService constructs a new SpoilerTTLService.
func NewSpoilerTTLService(
	repo domain.SpoilerTTLRepository,
	flashSvc *FlashDashService,
	logger *slog.Logger,
) *SpoilerTTLService {
	return &SpoilerTTLService{
		repo:     repo,
		flashSvc: flashSvc,
		logger:   logger,
		userTTLs: make(map[string]time.Duration),
	}
}

func userTTLKey(guildID, userID string) string {
	return guildID + ":" + userID
}

// ValidateSpoilerTTL checks that a standard user-selected duration is between 30 minutes and 14 days inclusive.
func ValidateSpoilerTTL(d time.Duration) error {
	return ValidateSpoilerTTLForUser(d, false)
}

// ValidateSpoilerTTLForUser checks that a selected duration is within valid bounds:
//   - Standard users: 30 minutes to 14 days inclusive
//   - Server Administrators: 1 minute to 14 days inclusive
func ValidateSpoilerTTLForUser(d time.Duration, isAdmin bool) error {
	if isAdmin {
		if d < domain.MinAdminSpoilerPhotoTTL {
			return fmt.Errorf("duration must be at least 1 minute (got %s)", FormatSpoilerTTL(d))
		}
	} else {
		if d < domain.MinSpoilerPhotoTTL {
			return fmt.Errorf("duration must be at least 30 minutes (got %s)", FormatSpoilerTTL(d))
		}
	}
	if d > domain.MaxSpoilerPhotoTTL {
		return fmt.Errorf("duration cannot exceed 14 days (got %s)", FormatSpoilerTTL(d))
	}
	return nil
}

// FormatSpoilerTTL formats a duration into a human-readable days/hours/minutes string.
func FormatSpoilerTTL(d time.Duration) string {
	if d <= 0 {
		return "0 minutes"
	}
	totalMinutes := int(d.Minutes())
	days := totalMinutes / (24 * 60)
	remMinutes := totalMinutes % (24 * 60)
	hours := remMinutes / 60
	minutes := remMinutes % 60

	var parts []string
	if days > 0 {
		if days == 1 {
			parts = append(parts, "1 day")
		} else {
			parts = append(parts, fmt.Sprintf("%d days", days))
		}
	}
	if hours > 0 {
		if hours == 1 {
			parts = append(parts, "1 hour")
		} else {
			parts = append(parts, fmt.Sprintf("%d hours", hours))
		}
	}
	if minutes > 0 || len(parts) == 0 {
		if minutes == 1 {
			parts = append(parts, "1 minute")
		} else {
			parts = append(parts, fmt.Sprintf("%d minutes", minutes))
		}
	}
	return strings.Join(parts, ", ")
}

// SplitSpoilerTTL decomposes a duration into (days, hours, minutes).
func SplitSpoilerTTL(d time.Duration) (days, hours, minutes int) {
	if d <= 0 {
		return 0, 0, 30
	}
	totalMinutes := int(d.Minutes())
	days = totalMinutes / (24 * 60)
	rem := totalMinutes % (24 * 60)
	hours = rem / 60
	minutes = rem % 60
	return days, hours, minutes
}

// Init loads all configured user spoiler TTL settings from the database and resumes pending message auto-deletion timers.
func (s *SpoilerTTLService) Init(ctx context.Context, session *discordgo.Session, guildID string) error {
	if s.repo == nil {
		return nil
	}

	// 1. Warm-load all configured user spoiler TTL settings
	settings, err := s.repo.ListUserSettings(ctx, guildID)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("failed to load user spoiler ttl settings from database", "error", err)
		}
	} else {
		s.mu.Lock()
		for _, st := range settings {
			s.userTTLs[userTTLKey(st.GuildID, st.UserID)] = st.Duration()
		}
		s.mu.Unlock()
		if s.logger != nil {
			s.logger.Info("loaded user spoiler ttl settings into memory", "count", len(settings))
		}
	}

	// 2. Process and resume pending spoiler message deletions
	pending, err := s.repo.ListPendingMessages(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("failed to list pending user spoiler messages from database", "error", err)
		}
		return nil
	}

	now := time.Now().UTC()
	for _, m := range pending {
		msg := m
		if msg.DeleteAt.Before(now) || msg.DeleteAt.Equal(now) {
			if session != nil {
				_ = session.ChannelMessageDelete(msg.ChannelID, msg.MessageID)
			}
			_ = s.repo.DeletePendingMessage(ctx, msg.ChannelID, msg.MessageID)
		} else {
			remaining := msg.DeleteAt.Sub(now)
			time.AfterFunc(remaining, func() {
				s.executeMessageDelete(session, msg.ChannelID, msg.MessageID)
			})
		}
	}

	return nil
}

// SetUserTTL saves and caches a standard user's personal spoilered photo TTL preference.
func (s *SpoilerTTLService) SetUserTTL(ctx context.Context, guildID, userID string, d time.Duration) error {
	return s.SetUserTTLWithAdmin(ctx, guildID, userID, d, false)
}

// SetUserTTLWithAdmin saves and caches a user's personal spoilered photo TTL preference,
// allowing Server Administrators to configure durations as low as 1 minute.
func (s *SpoilerTTLService) SetUserTTLWithAdmin(ctx context.Context, guildID, userID string, d time.Duration, isAdmin bool) error {
	if err := ValidateSpoilerTTLForUser(d, isAdmin); err != nil {
		return err
	}

	setting := &domain.UserSpoilerTTLSetting{
		GuildID:     guildID,
		UserID:      userID,
		DurationSec: int(d.Seconds()),
		UpdatedAt:   time.Now().UTC(),
	}

	if s.repo != nil {
		if err := s.repo.SaveUserSetting(ctx, setting); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.userTTLs[userTTLKey(guildID, userID)] = d
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.Info("configured user spoiler photo ttl",
			"guild_id", guildID,
			"user_id", userID,
			"duration", d.String(),
		)
	}
	return nil
}

// DisableUserTTL removes a user's personal spoilered photo TTL preference.
func (s *SpoilerTTLService) DisableUserTTL(ctx context.Context, guildID, userID string) error {
	if s.repo != nil {
		if err := s.repo.DeleteUserSetting(ctx, guildID, userID); err != nil {
			return err
		}
	}

	s.mu.Lock()
	delete(s.userTTLs, userTTLKey(guildID, userID))
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.Info("disabled user spoiler photo ttl", "guild_id", guildID, "user_id", userID)
	}
	return nil
}

// GetUserTTL returns the configured spoilered photo TTL for a user, if any.
func (s *SpoilerTTLService) GetUserTTL(guildID, userID string) (time.Duration, bool) {
	s.mu.RLock()
	d, ok := s.userTTLs[userTTLKey(guildID, userID)]
	s.mu.RUnlock()
	if ok && d > 0 {
		return d, true
	}

	if s.repo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		setting, err := s.repo.GetUserSetting(ctx, guildID, userID)
		if err == nil && setting != nil && setting.Duration() > 0 {
			s.mu.Lock()
			s.userTTLs[userTTLKey(guildID, userID)] = setting.Duration()
			s.mu.Unlock()
			return setting.Duration(), true
		}
	}

	return 0, false
}

// IsFlashDashChannel checks if the channel is already managed by FlashDashService.
func (s *SpoilerTTLService) IsFlashDashChannel(channelID string) bool {
	if s.flashSvc == nil {
		return false
	}
	return s.flashSvc.IsFlashDashChannel(channelID)
}

// ScheduleSpoilerMessageDeletion registers a spoilered photo message for auto-deletion in DB and starts a timer.
func (s *SpoilerTTLService) ScheduleSpoilerMessageDeletion(
	session *discordgo.Session,
	guildID, channelID, messageID, userID string,
	d time.Duration,
) {
	deleteAt := time.Now().UTC().Add(d)

	if s.repo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.repo.SavePendingMessage(ctx, &domain.UserSpoilerMessage{
			GuildID:   guildID,
			ChannelID: channelID,
			MessageID: messageID,
			UserID:    userID,
			DeleteAt:  deleteAt,
		})
		cancel()
	}

	time.AfterFunc(d, func() {
		s.executeMessageDelete(session, channelID, messageID)
	})
}

func (s *SpoilerTTLService) executeMessageDelete(session *discordgo.Session, channelID, messageID string) {
	if session != nil {
		if err := session.ChannelMessageDelete(channelID, messageID); err != nil {
			if s.logger != nil {
				s.logger.Debug("user spoiler message delete attempt", "channel_id", channelID, "message_id", messageID, "error", err)
			}
		}
	}

	if s.repo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.repo.DeletePendingMessage(ctx, channelID, messageID)
		cancel()
	}
}
