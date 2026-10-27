package service_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"syn/internal/domain"
	"syn/internal/service"
)

type mockFlashRepo struct {
	configs  map[string]*domain.FlashChannelConfig
	messages map[string]*domain.FlashMessage
}

func newMockFlashRepo() *mockFlashRepo {
	return &mockFlashRepo{
		configs:  make(map[string]*domain.FlashChannelConfig),
		messages: make(map[string]*domain.FlashMessage),
	}
}

func (m *mockFlashRepo) SaveChannelConfig(ctx context.Context, cfg *domain.FlashChannelConfig) error {
	m.configs[cfg.ChannelID] = cfg
	return nil
}

func (m *mockFlashRepo) GetChannelConfig(ctx context.Context, guildID, channelID string) (*domain.FlashChannelConfig, error) {
	if cfg, ok := m.configs[channelID]; ok && cfg.GuildID == guildID {
		return cfg, nil
	}
	return nil, nil
}

func (m *mockFlashRepo) ListChannelConfigs(ctx context.Context, guildID string) ([]*domain.FlashChannelConfig, error) {
	var list []*domain.FlashChannelConfig
	for _, cfg := range m.configs {
		if cfg.GuildID == guildID {
			list = append(list, cfg)
		}
	}
	return list, nil
}

func (m *mockFlashRepo) DeleteChannelConfig(ctx context.Context, guildID, channelID string) error {
	delete(m.configs, channelID)
	return nil
}

func (m *mockFlashRepo) SavePendingMessage(ctx context.Context, msg *domain.FlashMessage) error {
	m.messages[msg.MessageID] = msg
	return nil
}

func (m *mockFlashRepo) DeletePendingMessage(ctx context.Context, channelID, messageID string) error {
	delete(m.messages, messageID)
	return nil
}

func (m *mockFlashRepo) ListExpiredMessages(ctx context.Context, before time.Time) ([]*domain.FlashMessage, error) {
	var list []*domain.FlashMessage
	for _, msg := range m.messages {
		if msg.DeleteAt.Before(before) || msg.DeleteAt.Equal(before) {
			list = append(list, msg)
		}
	}
	return list, nil
}

func (m *mockFlashRepo) ListPendingMessages(ctx context.Context) ([]*domain.FlashMessage, error) {
	var list []*domain.FlashMessage
	for _, msg := range m.messages {
		list = append(list, msg)
	}
	return list, nil
}

func TestFlashDashService_ConfigureAndQuery(t *testing.T) {
	t.Parallel()

	repo := newMockFlashRepo()
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc := service.NewFlashDashService(repo, []string{"role_staff"}, silentLogger)

	ctx := context.Background()

	// 1. Duration validation: < 10s rejected
	if err := svc.ConfigureChannel(ctx, "guild_1", "chan_1", 5*time.Second); err == nil {
		t.Fatal("expected error for duration under 10s")
	}

	// 2. Configure valid channel
	if err := svc.ConfigureChannel(ctx, "guild_1", "chan_1", 30*time.Second); err != nil {
		t.Fatalf("unexpected error configuring channel: %v", err)
	}

	if !svc.IsFlashDashChannel("chan_1") {
		t.Fatal("expected chan_1 to be recognized as flash dash channel")
	}

	duration, ok := svc.GetChannelDuration("chan_1")
	if !ok || duration != 30*time.Second {
		t.Fatalf("expected 30s duration, got %v (ok=%v)", duration, ok)
	}

	// 3. Remove channel
	if err := svc.RemoveChannel(ctx, "guild_1", "chan_1"); err != nil {
		t.Fatalf("unexpected error removing channel: %v", err)
	}

	if svc.IsFlashDashChannel("chan_1") {
		t.Fatal("expected chan_1 to not be flash dash channel after removal")
	}
}

func TestFlashDashService_InitWithPendingMessages(t *testing.T) {
	t.Parallel()

	repo := newMockFlashRepo()
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc := service.NewFlashDashService(repo, []string{"role_staff"}, silentLogger)

	ctx := context.Background()

	// Pre-seed channel
	repo.configs["chan_flash"] = &domain.FlashChannelConfig{
		GuildID:     "guild_1",
		ChannelID:   "chan_flash",
		DurationSec: 60,
	}

	// Pre-seed an expired message
	repo.messages["msg_expired"] = &domain.FlashMessage{
		GuildID:   "guild_1",
		ChannelID: "chan_flash",
		MessageID: "msg_expired",
		DeleteAt:  time.Now().Add(-1 * time.Minute),
	}

	// Pre-seed a future message
	repo.messages["msg_future"] = &domain.FlashMessage{
		GuildID:   "guild_1",
		ChannelID: "chan_flash",
		MessageID: "msg_future",
		DeleteAt:  time.Now().Add(10 * time.Minute),
	}

	if err := svc.Init(ctx, nil, "guild_1"); err != nil {
		t.Fatalf("unexpected error during Init: %v", err)
	}

	// Channel should be loaded
	if !svc.IsFlashDashChannel("chan_flash") {
		t.Fatal("expected chan_flash to be loaded into memory")
	}

	// Expired message should be deleted from DB
	if _, ok := repo.messages["msg_expired"]; ok {
		t.Fatal("expected expired message to be removed from repository")
	}

	// Future message should still be in DB pending
	if _, ok := repo.messages["msg_future"]; !ok {
		t.Fatal("expected future message to remain in repository")
	}
}

