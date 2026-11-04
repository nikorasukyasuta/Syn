package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/handlers"
	"syn/internal/domain"
	"syn/internal/service"
)

type mockHoneypotRepo struct {
	mu          sync.Mutex
	cfg         *domain.HoneypotConfig
	quarantines map[string]*domain.SpamQuarantine
}

func newMockHoneypotRepo() *mockHoneypotRepo {
	return &mockHoneypotRepo{
		quarantines: make(map[string]*domain.SpamQuarantine),
	}
}

func (r *mockHoneypotRepo) SaveConfig(_ context.Context, cfg *domain.HoneypotConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *cfg
	r.cfg = &cp
	return nil
}

func (r *mockHoneypotRepo) GetConfig(_ context.Context, _ string) (*domain.HoneypotConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg == nil {
		return nil, nil
	}
	cp := *r.cfg
	return &cp, nil
}

func (r *mockHoneypotRepo) CreateQuarantine(_ context.Context, q *domain.SpamQuarantine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *q
	r.quarantines[q.UserID] = &cp
	return nil
}

func (r *mockHoneypotRepo) GetActiveQuarantine(_ context.Context, _, userID string) (*domain.SpamQuarantine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q, ok := r.quarantines[userID]
	if !ok || q.Status != domain.QuarantineActive {
		return nil, domain.ErrNotQuarantined
	}
	cp := *q
	return &cp, nil
}

func (r *mockHoneypotRepo) ListActiveQuarantines(_ context.Context, _ string) ([]*domain.SpamQuarantine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var list []*domain.SpamQuarantine
	for _, q := range r.quarantines {
		if q.Status == domain.QuarantineActive {
			cp := *q
			list = append(list, &cp)
		}
	}
	return list, nil
}

func (r *mockHoneypotRepo) ResolveQuarantine(_ context.Context, _, userID string, status domain.QuarantineStatus, resolvedBy string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q, ok := r.quarantines[userID]; ok {
		q.Status = status
		q.ResolvedBy = resolvedBy
	}
	return nil
}

func TestHoneypotCommandDefinitions(t *testing.T) {
	t.Parallel()

	hpCmd := handlers.HoneypotCommandDefinition()
	if hpCmd.Name != "honeypot" {
		t.Fatalf("expected command name 'honeypot', got %s", hpCmd.Name)
	}
	if len(hpCmd.Options) != 4 {
		t.Fatalf("expected 4 subcommands on /honeypot, got %d", len(hpCmd.Options))
	}

	unqCmd := handlers.UnquarantineCommandDefinition()
	if unqCmd.Name != "unquarantine" {
		t.Fatalf("expected command name 'unquarantine', got %s", unqCmd.Name)
	}
	if len(unqCmd.Options) != 1 || unqCmd.Options[0].Name != "user" {
		t.Fatalf("expected 'user' option on /unquarantine")
	}

	warnEmbed := handlers.BuildHoneypotWarningEmbed()
	if !strings.Contains(warnEmbed.Title, "DO NOT POST HERE") {
		t.Errorf("expected blatant warning title, got %q", warnEmbed.Title)
	}
	warnContent := handlers.BuildHoneypotWarningContent()
	if !strings.Contains(warnContent, "DO NOT TYPE IN THIS CHANNEL") {
		t.Errorf("expected blatant warning content, got %q", warnContent)
	}
}

func TestHoneypotHandler_MessageTrapAndStaffRelease(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	repo := newMockHoneypotRepo()
	svc := service.NewHoneypotService(repo, nil, silentLogger)

	cfg := &config.Config{
		GuildID:             "guild_1",
		StaffRoleIDs:        []string{"role_staff"},
		StaffAlertChannelID: "chan_staff_alerts",
		HoneypotChannelID:   "chan_honeypot",
		SpammerRoleID:       "role_spammer",
	}

	var deletedMsgID string
	s, _ := discordgo.New("Bot mock-token")
	s.Client.Transport = mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		path := req.URL.Path
		method := req.Method

		if method == http.MethodDelete && strings.Contains(path, "/channels/chan_honeypot/messages/") {
			parts := strings.Split(path, "/")
			deletedMsgID = parts[len(parts)-1]
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}
		if method == http.MethodGet && strings.HasSuffix(path, "/members/user_spammer") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"user":{"id":"user_spammer","username":"spambot"},"roles":["role_verified"]}`)),
				Header:     make(http.Header),
			}, nil
		}
		if method == http.MethodGet && strings.HasSuffix(path, "/roles") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`[{"id":"role_verified","name":"Verified"},{"id":"role_spammer","name":"Spammer"}]`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"123"}`)),
			Header:     make(http.Header),
		}, nil
	})

	if err := svc.Init(ctx, s, "guild_1", "chan_honeypot", "role_spammer"); err != nil {
		t.Fatalf("svc.Init failed: %v", err)
	}

	h := handlers.NewHoneypotHandler(svc, cfg, nil, nil, silentLogger)

	// 1. Staff member typing in honeypot channel should NOT be quarantined
	h.HandleMessageCreate(s, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_staff_1",
			ChannelID: "chan_honeypot",
			GuildID:   "guild_1",
			Content:   "testing channel",
			Author:    &discordgo.User{ID: "staff_user", Username: "admin"},
			Member:    &discordgo.Member{Roles: []string{"role_staff"}},
		},
	})
	if svc.GetActive("staff_user") != nil {
		t.Fatalf("expected staff member NOT to be quarantined")
	}
	if deletedMsgID != "msg_staff_1" {
		t.Errorf("expected staff message in honeypot to still be deleted to keep channel clean, got %q", deletedMsgID)
	}

	// 2. Non-staff spammer typing in honeypot channel SHOULD be quarantined and message deleted
	h.HandleMessageCreate(s, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_spam_99",
			ChannelID: "chan_honeypot",
			GuildID:   "guild_1",
			Content:   "free nitro click here",
			Author:    &discordgo.User{ID: "user_spammer", Username: "spambot"},
			Member:    &discordgo.Member{Roles: []string{"role_verified"}},
		},
	})

	if deletedMsgID != "msg_spam_99" {
		t.Errorf("expected spam message msg_spam_99 to be deleted, got %q", deletedMsgID)
	}
	q := svc.GetActive("user_spammer")
	if q == nil {
		t.Fatalf("expected user_spammer to be placed in active quarantine")
	}
	if len(q.RemovedRoleIDs) != 1 || q.RemovedRoleIDs[0] != "role_verified" {
		t.Errorf("expected role_verified to be recorded in RemovedRoleIDs, got %v", q.RemovedRoleIDs)
	}

	// 3. Staff runs /unquarantine user:user_spammer
	unqInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_unq_1",
			Token:   "tok_unq_1",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild_1",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_user"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "unquarantine",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "user",
						Type:  discordgo.ApplicationCommandOptionUser,
						Value: "user_spammer",
					},
				},
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Users: map[string]*discordgo.User{
						"user_spammer": {ID: "user_spammer", Username: "spambot"},
					},
				},
			},
		},
	}

	h.HandleUnquarantineCommand(s, unqInteraction)

	if svc.GetActive("user_spammer") != nil {
		t.Fatalf("expected user_spammer to be released from active quarantine after /unquarantine")
	}
}

func TestHoneypotHandler_ReleaseButtonInteraction(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	repo := newMockHoneypotRepo()
	svc := service.NewHoneypotService(repo, nil, silentLogger)

	cfg := &config.Config{
		GuildID:           "guild_1",
		StaffRoleIDs:      []string{"role_staff"},
		HoneypotChannelID: "chan_honeypot",
		SpammerRoleID:     "role_spammer",
	}

	s, _ := discordgo.New("Bot mock-token")
	s.Client.Transport = mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/members/user_2") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"user":{"id":"user_2","username":"human"},"roles":["role_verified"]}`)),
				Header:     make(http.Header),
			}, nil
		}
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/roles") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`[{"id":"role_verified","name":"Verified"},{"id":"role_spammer","name":"Spammer"}]`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"123"}`)),
			Header:     make(http.Header),
		}, nil
	})

	_ = svc.Init(ctx, s, "guild_1", "chan_honeypot", "role_spammer")
	h := handlers.NewHoneypotHandler(svc, cfg, nil, nil, silentLogger)

	if _, _, err := svc.Quarantine(ctx, "guild_1", "user_2", "human", "chan_honeypot", "msg_2"); err != nil {
		t.Fatalf("Quarantine failed: %v", err)
	}

	btnInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_btn_1",
			Token:     "tok_btn_1",
			Type:      discordgo.InteractionMessageComponent,
			GuildID:   "guild_1",
			ChannelID: "chan_staff_alerts",
			Message:   &discordgo.Message{ID: "alert_msg_1"},
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_1"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.MessageComponentInteractionData{
				CustomID:      handlers.HoneypotReleaseButtonPrefix + "user_2",
				ComponentType: discordgo.ButtonComponent,
			},
		},
	}

	handled := h.HandleComponentInteraction(s, btnInteraction)
	if !handled {
		t.Fatalf("expected HandleComponentInteraction to handle %s", handlers.HoneypotReleaseButtonPrefix+"user_2")
	}
	if svc.GetActive("user_2") != nil {
		t.Fatalf("expected user_2 to be released via button interaction")
	}
}

func TestHoneypotHandler_PurgeRecentUserMessages(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	repo := newMockHoneypotRepo()
	svc := service.NewHoneypotService(repo, nil, silentLogger)

	cfg := &config.Config{
		GuildID:           "guild_1",
		StaffRoleIDs:      []string{"role_staff"},
		HoneypotChannelID: "chan_honeypot",
		SpammerRoleID:     "role_spammer",
	}

	var deletedIDs []string
	var mu sync.Mutex

	s, _ := discordgo.New("Bot mock-token")
	s.Client.Transport = mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		path := req.URL.Path
		method := req.Method

		// Return channels: general-chat
		if method == http.MethodGet && strings.HasSuffix(path, "/guilds/guild_1/channels") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`[{"id":"chan_general","type":0,"name":"general"}]`)),
				Header:     make(http.Header),
			}, nil
		}

		// Return messages in general-chat: 1 from spammer within 5 min, 1 from spammer older than 5 min, 1 from regular user
		if method == http.MethodGet && strings.HasSuffix(path, "/channels/chan_general/messages") {
			recentTime := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
			oldTime := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
			jsonResp := `[
				{"id":"msg_spammer_recent","channel_id":"chan_general","author":{"id":"user_spammer"},"timestamp":"` + recentTime + `"},
				{"id":"msg_spammer_old","channel_id":"chan_general","author":{"id":"user_spammer"},"timestamp":"` + oldTime + `"},
				{"id":"msg_innocent_user","channel_id":"chan_general","author":{"id":"user_innocent"},"timestamp":"` + recentTime + `"}
			]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(jsonResp)),
				Header:     make(http.Header),
			}, nil
		}

		// Track deleted message IDs
		if method == http.MethodDelete && strings.Contains(path, "/channels/chan_general/messages/") {
			parts := strings.Split(path, "/")
			mu.Lock()
			deletedIDs = append(deletedIDs, parts[len(parts)-1])
			mu.Unlock()
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}

		// Bulk delete
		if method == http.MethodPost && strings.HasSuffix(path, "/bulk-delete") {
			mu.Lock()
			deletedIDs = append(deletedIDs, "bulk_deleted")
			mu.Unlock()
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	h := handlers.NewHoneypotHandler(svc, cfg, nil, nil, silentLogger)
	h.PurgeRecentUserMessages(s, "guild_1", "user_spammer", 5*time.Minute)

	mu.Lock()
	defer mu.Unlock()
	if len(deletedIDs) != 1 || deletedIDs[0] != "msg_spammer_recent" {
		t.Errorf("expected only 'msg_spammer_recent' to be deleted, got %v", deletedIDs)
	}
}

