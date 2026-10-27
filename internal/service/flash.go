package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/domain"
)

// FlashDashService manages Flash and Dash ephemeral photo channels, auto-spoilering,
// and configurable TTL auto-deletion timers.
type FlashDashService struct {
	repo         domain.FlashRepository
	staffRoleIDs []string
	logger       *slog.Logger

	mu       sync.RWMutex
	channels map[string]time.Duration // channel_id -> duration
}

// NewFlashDashService constructs a new FlashDashService.
func NewFlashDashService(
	repo domain.FlashRepository,
	staffRoleIDs []string,
	logger *slog.Logger,
) *FlashDashService {
	return &FlashDashService{
		repo:         repo,
		staffRoleIDs: staffRoleIDs,
		logger:       logger,
		channels:     make(map[string]time.Duration),
	}
}

// Init loads all configured channels from the database and resumes pending message auto-deletion timers.
func (s *FlashDashService) Init(ctx context.Context, session *discordgo.Session, guildID string) error {
	if s.repo == nil {
		return nil
	}

	// 1. Warm-load all configured channels
	configs, err := s.repo.ListChannelConfigs(ctx, guildID)
	if err != nil {
		s.logger.Error("failed to load flash channel configs from database", "error", err)
	} else {
		s.mu.Lock()
		for _, c := range configs {
			s.channels[c.ChannelID] = c.Duration()
		}
		s.mu.Unlock()
		s.logger.Info("loaded flash and dash channels into memory", "count", len(configs))
	}

	// 2. Process and resume pending message deletions
	pending, err := s.repo.ListPendingMessages(ctx)
	if err != nil {
		s.logger.Error("failed to list pending flash messages from database", "error", err)
		return nil
	}

	now := time.Now().UTC()
	for _, m := range pending {
		msg := m
		if msg.DeleteAt.Before(now) || msg.DeleteAt.Equal(now) {
			// Already expired while bot was offline: delete immediately
			if session != nil {
				_ = session.ChannelMessageDelete(msg.ChannelID, msg.MessageID)
			}
			_ = s.repo.DeletePendingMessage(ctx, msg.ChannelID, msg.MessageID)
		} else {
			// Schedule deletion for remaining duration
			remaining := msg.DeleteAt.Sub(now)
			time.AfterFunc(remaining, func() {
				s.executeMessageDelete(session, msg.ChannelID, msg.MessageID)
			})
		}
	}

	return nil
}

// ConfigureChannel binds a channel to Flash and Dash auto-deletion with the given TTL.
func (s *FlashDashService) ConfigureChannel(ctx context.Context, guildID, channelID string, d time.Duration) error {
	if d < 10*time.Second {
		return fmt.Errorf("duration must be at least 10 seconds")
	}

	sec := int(d.Seconds())
	cfg := &domain.FlashChannelConfig{
		GuildID:     guildID,
		ChannelID:   channelID,
		DurationSec: sec,
	}

	if s.repo != nil {
		if err := s.repo.SaveChannelConfig(ctx, cfg); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.channels[channelID] = d
	s.mu.Unlock()

	s.logger.Info("configured flash and dash channel",
		"guild_id", guildID,
		"channel_id", channelID,
		"duration", d.String(),
	)
	return nil
}

// RemoveChannel disables Flash and Dash auto-deletion for a channel.
func (s *FlashDashService) RemoveChannel(ctx context.Context, guildID, channelID string) error {
	if s.repo != nil {
		if err := s.repo.DeleteChannelConfig(ctx, guildID, channelID); err != nil {
			return err
		}
	}

	s.mu.Lock()
	delete(s.channels, channelID)
	s.mu.Unlock()

	s.logger.Info("disabled flash and dash channel", "guild_id", guildID, "channel_id", channelID)
	return nil
}

// IsFlashDashChannel checks if a channel is configured for Flash and Dash.
func (s *FlashDashService) IsFlashDashChannel(channelID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.channels[channelID]
	return ok
}

// GetChannelDuration returns the configured TTL for a Flash and Dash channel.
func (s *FlashDashService) GetChannelDuration(channelID string) (time.Duration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.channels[channelID]
	return d, ok
}

// ListChannels returns all configured Flash and Dash channels for a guild.
func (s *FlashDashService) ListChannels(ctx context.Context, guildID string) ([]*domain.FlashChannelConfig, error) {
	if s.repo != nil {
		return s.repo.ListChannelConfigs(ctx, guildID)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	var list []*domain.FlashChannelConfig
	for chID, d := range s.channels {
		list = append(list, &domain.FlashChannelConfig{
			GuildID:     guildID,
			ChannelID:   chID,
			DurationSec: int(d.Seconds()),
		})
	}
	return list, nil
}

// ScheduleMessageDeletion registers a message for auto-deletion in DB and starts a timer.
func (s *FlashDashService) ScheduleMessageDeletion(
	session *discordgo.Session,
	guildID, channelID, messageID string,
	d time.Duration,
) {
	deleteAt := time.Now().UTC().Add(d)

	if s.repo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.repo.SavePendingMessage(ctx, &domain.FlashMessage{
			GuildID:   guildID,
			ChannelID: channelID,
			MessageID: messageID,
			DeleteAt:  deleteAt,
		})
		cancel()
	}

	time.AfterFunc(d, func() {
		s.executeMessageDelete(session, channelID, messageID)
	})
}

func (s *FlashDashService) executeMessageDelete(session *discordgo.Session, channelID, messageID string) {
	if session != nil {
		if err := session.ChannelMessageDelete(channelID, messageID); err != nil {
			s.logger.Debug("flash message delete attempt", "channel_id", channelID, "message_id", messageID, "error", err)
		}
	}

	if s.repo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.repo.DeletePendingMessage(ctx, channelID, messageID)
		cancel()
	}
}

// IsStaff checks if the member has any staff or helper roles.
func (s *FlashDashService) IsStaff(callerRoles []string) bool {
	if len(s.staffRoleIDs) == 0 {
		return false
	}
	staffMap := make(map[string]struct{}, len(s.staffRoleIDs))
	for _, id := range s.staffRoleIDs {
		staffMap[id] = struct{}{}
	}
	for _, r := range callerRoles {
		if _, ok := staffMap[r]; ok {
			return true
		}
	}
	return false
}

