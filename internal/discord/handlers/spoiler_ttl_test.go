package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/handlers"
	"syn/internal/domain"
	"syn/internal/service"
)

type mockSpoilerTTLRepo struct {
	mu       sync.Mutex
	settings map[string]*domain.UserSpoilerTTLSetting
	messages map[string]*domain.UserSpoilerMessage
}

func newMockSpoilerTTLRepo() *mockSpoilerTTLRepo {
	return &mockSpoilerTTLRepo{
		settings: make(map[string]*domain.UserSpoilerTTLSetting),
		messages: make(map[string]*domain.UserSpoilerMessage),
	}
}

func (m *mockSpoilerTTLRepo) SaveUserSetting(_ context.Context, setting *domain.UserSpoilerTTLSetting) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[setting.GuildID+":"+setting.UserID] = setting
	return nil
}

func (m *mockSpoilerTTLRepo) GetUserSetting(_ context.Context, guildID, userID string) (*domain.UserSpoilerTTLSetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings[guildID+":"+userID], nil
}

func (m *mockSpoilerTTLRepo) ListUserSettings(_ context.Context, guildID string) ([]*domain.UserSpoilerTTLSetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []*domain.UserSpoilerTTLSetting
	for _, s := range m.settings {
		if s.GuildID == guildID {
			list = append(list, s)
		}
	}
	return list, nil
}

func (m *mockSpoilerTTLRepo) DeleteUserSetting(_ context.Context, guildID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.settings, guildID+":"+userID)
	return nil
}

func (m *mockSpoilerTTLRepo) SavePendingMessage(_ context.Context, msg *domain.UserSpoilerMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[msg.MessageID] = msg
	return nil
}

func (m *mockSpoilerTTLRepo) DeletePendingMessage(_ context.Context, _, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.messages, messageID)
	return nil
}

func (m *mockSpoilerTTLRepo) ListExpiredMessages(_ context.Context, before time.Time) ([]*domain.UserSpoilerMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []*domain.UserSpoilerMessage
	for _, msg := range m.messages {
		if msg.DeleteAt.Before(before) || msg.DeleteAt.Equal(before) {
			list = append(list, msg)
		}
	}
	return list, nil
}

func (m *mockSpoilerTTLRepo) ListPendingMessages(_ context.Context) ([]*domain.UserSpoilerMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []*domain.UserSpoilerMessage
	for _, msg := range m.messages {
		list = append(list, msg)
	}
	return list, nil
}

func (m *mockSpoilerTTLRepo) hasMessage(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.messages[id] != nil
}

func TestSpoilerTTL_ValidationAndBounds(t *testing.T) {
	t.Parallel()

	// Minimum is 30 minutes
	if err := service.ValidateSpoilerTTL(29 * time.Minute); err == nil {
		t.Fatal("expected 29m to be rejected (minimum is 30m)")
	}
	if err := service.ValidateSpoilerTTL(30 * time.Minute); err != nil {
		t.Fatalf("expected 30m to be valid, got: %v", err)
	}

	// Arbitrary duration between 30m and 14d
	if err := service.ValidateSpoilerTTL(2*24*time.Hour + 4*time.Hour + 17*time.Minute); err != nil {
		t.Fatalf("expected 2d 4h 17m to be valid, got: %v", err)
	}

	// Maximum is 14 days
	if err := service.ValidateSpoilerTTL(14 * 24 * time.Hour); err != nil {
		t.Fatalf("expected 14d to be valid, got: %v", err)
	}
	if err := service.ValidateSpoilerTTL(14*24*time.Hour + 1*time.Minute); err == nil {
		t.Fatal("expected 14d 1m to be rejected (maximum is 14d)")
	}
}

func TestSpoilerTTLHandler_EphemeralPromptDropdownsAndConfirmButton(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := newMockSpoilerTTLRepo()
	flashRepo := newMockFlashRepo()
	flashSvc := service.NewFlashDashService(flashRepo, nil, logger)
	svc := service.NewSpoilerTTLService(repo, flashSvc, logger)
	cfg := &config.Config{GuildID: "guild_123"}
	handler := handlers.NewSpoilerTTLHandler(svc, cfg, logger)

	session := newMockSession(http.StatusOK, `{}`)

	// 1. User runs /spoiler-ttl -> opens ephemeral prompt
	openCmd := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_open",
			GuildID: "guild_123",
			Type:    discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "user_99"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "spoiler-ttl",
			},
		},
	}
	handler.HandleSpoilerTTLCommand(session, openCmd)

	// 2. User selects 2 days, 3 hours, 45 minutes via the 3 dropdown menus
	for _, step := range []struct {
		customID string
		val      string
	}{
		{handlers.SelectSpoilerTTLDaysCustomID, "2"},
		{handlers.SelectSpoilerTTLHoursCustomID, "3"},
		{handlers.SelectSpoilerTTLMinutesCustomID, "45"},
	} {
		compInt := &discordgo.InteractionCreate{
			Interaction: &discordgo.Interaction{
				ID:      "int_select_" + step.customID,
				GuildID: "guild_123",
				Type:    discordgo.InteractionMessageComponent,
				Member: &discordgo.Member{
					User: &discordgo.User{ID: "user_99"},
				},
				Data: discordgo.MessageComponentInteractionData{
					CustomID:      step.customID,
					ComponentType: discordgo.SelectMenuComponent,
					Values:        []string{step.val},
				},
			},
		}
		if !handler.HandleComponentInteraction(session, compInt) {
			t.Fatalf("expected component interaction %s to be handled", step.customID)
		}
	}

	// 3. User clicks the green confirmation button (btn_spoiler_ttl_confirm)
	confirmInt := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_confirm",
			GuildID: "guild_123",
			Type:    discordgo.InteractionMessageComponent,
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "user_99"},
			},
			Data: discordgo.MessageComponentInteractionData{
				CustomID:      handlers.BtnSpoilerTTLConfirmCustomID,
				ComponentType: discordgo.ButtonComponent,
			},
		},
	}
	if !handler.HandleComponentInteraction(session, confirmInt) {
		t.Fatal("expected confirm button interaction to be handled")
	}

	expectedTTL := 2*24*time.Hour + 3*time.Hour + 45*time.Minute
	savedTTL, ok := svc.GetUserTTL("guild_123", "user_99")
	if !ok || savedTTL != expectedTTL {
		t.Fatalf("expected saved TTL %v, got %v (ok=%v)", expectedTTL, savedTTL, ok)
	}

	// 4. User posts a spoilered photo in a normal channel -> scheduled for deletion
	spoilerMsg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_spoiler_nsfw_1",
			ChannelID: "ch_general_nsfw",
			GuildID:   "guild_123",
			Author:    &discordgo.User{ID: "user_99"},
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att_nsfw",
					Filename:    "SPOILER_photo.jpg",
					ContentType: "image/jpeg",
				},
			},
		},
	}
	handler.HandleMessageCreate(session, spoilerMsg)
	if !repo.hasMessage("msg_spoiler_nsfw_1") {
		t.Fatal("expected spoilered photo message to be scheduled for auto-deletion")
	}

	// 5. User posts an unspoilered photo -> NOT scheduled by SpoilerTTLHandler
	unspoileredMsg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_normal_photo_2",
			ChannelID: "ch_general_nsfw",
			GuildID:   "guild_123",
			Author:    &discordgo.User{ID: "user_99"},
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att_normal",
					Filename:    "cat.jpg",
					ContentType: "image/jpeg",
				},
			},
		},
	}
	handler.HandleMessageCreate(session, unspoileredMsg)
	if repo.hasMessage("msg_normal_photo_2") {
		t.Fatal("did not expect unspoilered photo to be scheduled by SpoilerTTLHandler")
	}
}

