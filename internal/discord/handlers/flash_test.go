package handlers_test

import (
	"bytes"
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

type mockFlashRepo struct {
	mu       sync.Mutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[cfg.ChannelID] = cfg
	return nil
}

func (m *mockFlashRepo) GetChannelConfig(ctx context.Context, guildID, channelID string) (*domain.FlashChannelConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configs[channelID], nil
}

func (m *mockFlashRepo) ListChannelConfigs(ctx context.Context, guildID string) ([]*domain.FlashChannelConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []*domain.FlashChannelConfig
	for _, c := range m.configs {
		list = append(list, c)
	}
	return list, nil
}

func (m *mockFlashRepo) DeleteChannelConfig(ctx context.Context, guildID, channelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, channelID)
	return nil
}

func (m *mockFlashRepo) SavePendingMessage(ctx context.Context, msg *domain.FlashMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[msg.MessageID] = msg
	return nil
}

func (m *mockFlashRepo) DeletePendingMessage(ctx context.Context, channelID, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.messages, messageID)
	return nil
}

func (m *mockFlashRepo) ListExpiredMessages(ctx context.Context, before time.Time) ([]*domain.FlashMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []*domain.FlashMessage
	for _, msg := range m.messages {
		if msg.DeleteAt.Before(before) {
			list = append(list, msg)
		}
	}
	return list, nil
}

func (m *mockFlashRepo) ListPendingMessages(ctx context.Context) ([]*domain.FlashMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []*domain.FlashMessage
	for _, msg := range m.messages {
		list = append(list, msg)
	}
	return list, nil
}

type mockHTTPClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.doFunc != nil {
		return m.doFunc(req)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte("fake image data"))),
	}, nil
}

func TestParseFlashDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"10s", 10 * time.Second, false},
		{"30s", 30 * time.Second, false},
		{"5m", 5 * time.Minute, false},
		{"1h", 1 * time.Hour, false},
		{"2d", 48 * time.Hour, false},
		{"60", 60 * time.Second, false},
		{"5s", 0, true}, // < 10s
		{"5", 0, true},  // < 10s
		{"", 0, true},
		{"abc", 0, true},
		{"0d", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			res, err := handlers.ParseFlashDuration(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for input %q, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if !tc.wantErr && res != tc.expected {
				t.Fatalf("expected duration %v, got %v", tc.expected, res)
			}
		})
	}
}

func TestFlashCommandDefinition(t *testing.T) {
	cmd := handlers.FlashCommandDefinition()
	if cmd.Name != "flash" {
		t.Fatalf("expected command name 'flash', got %s", cmd.Name)
	}
	if len(cmd.Options) != 3 {
		t.Fatalf("expected 3 subcommands, got %d", len(cmd.Options))
	}
	subcommands := map[string]bool{}
	for _, opt := range cmd.Options {
		subcommands[opt.Name] = true
	}
	if !subcommands["set-timer"] || !subcommands["disable"] || !subcommands["status"] {
		t.Fatalf("missing required subcommands: %+v", subcommands)
	}
}

func TestFlashHandler_SetTimerAndDisable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := newMockFlashRepo()
	svc := service.NewFlashDashService(repo, []string{"role_staff"}, logger)
	cfg := &config.Config{GuildID: "guild_123"}
	handler := handlers.NewFlashHandler(svc, cfg, logger)

	session := newMockSession(http.StatusOK, `{}`)

	// 1. Run /flash set-timer duration:5m channel:ch_test
	setTimerInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_1",
			ChannelID: "ch_test",
			GuildID:   "guild_123",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "flash",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "set-timer",
						Type: discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{
							{
								Name:  "duration",
								Type:  discordgo.ApplicationCommandOptionString,
								Value: "5m",
							},
						},
					},
				},
			},
		},
	}

	handler.HandleFlashCommand(session, setTimerInteraction)

	d, ok := svc.GetChannelDuration("ch_test")
	if !ok {
		t.Fatal("expected channel 'ch_test' to be registered as Flash and Dash channel")
	}
	if d != 5*time.Minute {
		t.Fatalf("expected 5m duration, got %v", d)
	}

	// 2. Run /flash status
	statusInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_2",
			ChannelID: "ch_test",
			GuildID:   "guild_123",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "flash",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "status",
						Type: discordgo.ApplicationCommandOptionSubCommand,
					},
				},
			},
		},
	}
	handler.HandleFlashCommand(session, statusInteraction)

	// 3. Run /flash disable
	disableInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_3",
			ChannelID: "ch_test",
			GuildID:   "guild_123",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "flash",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "disable",
						Type: discordgo.ApplicationCommandOptionSubCommand,
					},
				},
			},
		},
	}
	handler.HandleFlashCommand(session, disableInteraction)

	_, okAfter := svc.GetChannelDuration("ch_test")
	if okAfter {
		t.Fatal("expected channel 'ch_test' to no longer be configured after disable")
	}
}

func TestFlashHandler_HandleMessageCreate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := newMockFlashRepo()
	svc := service.NewFlashDashService(repo, []string{"role_staff"}, logger)
	cfg := &config.Config{GuildID: "guild_123"}
	handler := handlers.NewFlashHandler(svc, cfg, logger)

	// Set custom HTTP client for downloading mock attachment
	handler.SetHTTPClient(&mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte("mock image content"))),
			}, nil
		},
	})

	ctx := context.Background()
	_ = svc.ConfigureChannel(ctx, "guild_123", "ch_flash", 15*time.Second)

	// Mock session that returns a mock message ID when re-posting
	session := newMockSession(http.StatusOK, `{"id": "msg_reposted_456"}`)

	// Case 1: Bot message is ignored
	botMsg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_bot",
			ChannelID: "ch_flash",
			GuildID:   "guild_123",
			Author:    &discordgo.User{ID: "bot_id", Bot: true},
		},
	}
	handler.HandleMessageCreate(session, botMsg)

	// Case 2: Message in non-flash channel is ignored
	nonFlashMsg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_other",
			ChannelID: "ch_regular",
			GuildID:   "guild_123",
			Author:    &discordgo.User{ID: "user_123"},
		},
	}
	handler.HandleMessageCreate(session, nonFlashMsg)

	// Case 3: Already spoilered message is scheduled for deletion directly
	spoileredMsg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_spoilered",
			ChannelID: "ch_flash",
			GuildID:   "guild_123",
			Author:    &discordgo.User{ID: "user_123"},
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:       "att_1",
					Filename: "SPOILER_photo.png",
					URL:      "http://example.com/photo.png",
				},
			},
		},
	}
	handler.HandleMessageCreate(session, spoileredMsg)

	// Give a moment for goroutine
	time.Sleep(50 * time.Millisecond)
	if repo.messages["msg_spoilered"] == nil {
		t.Errorf("expected already-spoilered message 'msg_spoilered' to be scheduled in repository")
	}

	// Case 4: Unspoilered message is auto-spoilered
	unspoileredMsg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_unspoilered",
			ChannelID: "ch_flash",
			GuildID:   "guild_123",
			Author:    &discordgo.User{ID: "user_456"},
			Content:   "Look at this!",
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:          "att_2",
					Filename:    "vacation.jpg",
					ContentType: "image/jpeg",
					URL:         "http://example.com/vacation.jpg",
				},
			},
		},
	}
	handler.HandleMessageCreate(session, unspoileredMsg)

	// Give goroutine time to download and re-post
	time.Sleep(100 * time.Millisecond)

	// Check if re-posted message was scheduled
	if repo.messages["msg_reposted_456"] == nil {
		t.Errorf("expected reposted spoilered message 'msg_reposted_456' to be scheduled in repository")
	}
}