package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/handlers"
	"syn/internal/domain"
	"syn/internal/service"
)

type mockFilterWarningRepo struct {
	warnings   []*domain.UserWarning
	violations map[string]*domain.FilterViolation
}

func newMockFilterWarningRepo() *mockFilterWarningRepo {
	return &mockFilterWarningRepo{
		violations: make(map[string]*domain.FilterViolation),
	}
}

func (m *mockFilterWarningRepo) SaveWarning(ctx context.Context, w *domain.UserWarning) error {
	w.ID = "warn_test_1"
	w.CreatedAt = time.Now().UTC()
	m.warnings = append(m.warnings, w)
	return nil
}

func (m *mockFilterWarningRepo) GetWarningsByUserID(ctx context.Context, guildID, userID string) ([]*domain.UserWarning, error) {
	var res []*domain.UserWarning
	for _, w := range m.warnings {
		if w.GuildID == guildID && w.UserID == userID {
			res = append(res, w)
		}
	}
	return res, nil
}

func (m *mockFilterWarningRepo) GetWarningCountByUserID(ctx context.Context, guildID, userID string) (int, error) {
	count := 0
	for _, w := range m.warnings {
		if w.GuildID == guildID && w.UserID == userID {
			count++
		}
	}
	return count, nil
}

func (m *mockFilterWarningRepo) SaveViolation(ctx context.Context, v *domain.FilterViolation) error {
	v.ID = "viol_test_1"
	v.CreatedAt = time.Now().UTC()
	m.violations[v.ChannelID] = v
	return nil
}

func (m *mockFilterWarningRepo) GetViolationByChannelID(ctx context.Context, guildID, channelID string) (*domain.FilterViolation, error) {
	if v, ok := m.violations[channelID]; ok && v.GuildID == guildID {
		return v, nil
	}
	return nil, nil
}

type mockTicketRepo struct {
	ticketsByChannel map[string]*domain.Ticket
}

func newMockTicketRepo() *mockTicketRepo {
	return &mockTicketRepo{
		ticketsByChannel: make(map[string]*domain.Ticket),
	}
}

func (m *mockTicketRepo) CreateTicket(ctx context.Context, t *domain.Ticket) error {
	t.ID = "mock_uuid_" + t.ChannelID
	m.ticketsByChannel[t.ChannelID] = t
	return nil
}

func (m *mockTicketRepo) GetTicketByChannelID(ctx context.Context, guildID, channelID string) (*domain.Ticket, error) {
	t, ok := m.ticketsByChannel[channelID]
	if !ok || t.GuildID != guildID {
		return nil, domain.ErrTicketNotFound
	}
	return t, nil
}

func (m *mockTicketRepo) GetActiveTicket(ctx context.Context, guildID, creatorID string, ticketType domain.TicketType) (*domain.Ticket, error) {
	for _, t := range m.ticketsByChannel {
		if t.GuildID == guildID && t.CreatorID == creatorID && t.Type == ticketType && t.Status == domain.TicketStatusOpen {
			return t, nil
		}
	}
	return nil, nil
}

func (m *mockTicketRepo) CloseTicket(ctx context.Context, guildID, channelID, closedByID, resolution string, closedAt time.Time) error {
	t, ok := m.ticketsByChannel[channelID]
	if !ok {
		return domain.ErrTicketNotFound
	}
	t.Status = domain.TicketStatusClosed
	t.ClosedByID = closedByID
	t.Resolution = resolution
	t.ClosedAt = &closedAt
	return nil
}

func (m *mockTicketRepo) ListOpenTicketsByCreator(ctx context.Context, guildID, creatorID string) ([]*domain.Ticket, error) {
	return nil, nil
}

func TestFilterCommandDefinitions(t *testing.T) {
	t.Parallel()

	fCmd := handlers.FilterCommandDefinition()
	if fCmd.Name != "filter" {
		t.Fatalf("expected command name 'filter', got %s", fCmd.Name)
	}

	wCmd := handlers.WarningsCommandDefinition()
	if wCmd.Name != "warnings" {
		t.Fatalf("expected command name 'warnings', got %s", wCmd.Name)
	}
}

func TestFilterHandler_HandleFilterCommand(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	globalFile := filepath.Join(tmpDir, "global.json")
	if err := os.WriteFile(globalFile, []byte(`["badword1", "badword2"]`), 0644); err != nil {
		t.Fatal(err)
	}

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		GuildID:      "guild_1",
		StaffRoleIDs: []string{"role_staff_admin"},
		FiltersDir:   tmpDir,
	}

	warningRepo := newMockFilterWarningRepo()
	filterSvc := service.NewWordFilterService(warningRepo, cfg.StaffRoleIDs, silentLogger)
	ticketSvc := service.NewTicketService(nil, cfg.StaffRoleIDs, silentLogger)
	handler := handlers.NewFilterHandler(filterSvc, ticketSvc, cfg, nil, silentLogger)

	session := newMockSession(http.StatusOK, `{}`)

	// 1. Non-staff attempt -> blocked
	nonStaffInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_reload_1",
			Token: "tok_1",
			Type:  discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "regular_user"},
				Roles: []string{"role_regular"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "reload",
						Type: discordgo.ApplicationCommandOptionSubCommand,
					},
				},
			},
		},
	}
	handler.HandleFilterCommand(session, nonStaffInteraction)

	// 2. Staff reload global filter -> success
	staffGlobalInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_reload_2",
			Token: "tok_2",
			Type:  discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "admin_user"},
				Roles: []string{"role_staff_admin"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "reload",
						Type: discordgo.ApplicationCommandOptionSubCommand,
					},
				},
			},
		},
	}
	handler.HandleFilterCommand(session, staffGlobalInteraction)
	if len(filterSvc.GetGlobalWords()) != 2 {
		t.Fatalf("expected 2 global words in cache, got %d", len(filterSvc.GetGlobalWords()))
	}

	// 3. Staff reload missing channel filter -> preserves cache, does not fail
	staffChannelInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_reload_3",
			Token: "tok_3",
			Type:  discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "admin_user"},
				Roles: []string{"role_staff_admin"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "reload",
						Type: discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{
							{
								Name:  "channel",
								Type:  discordgo.ApplicationCommandOptionChannel,
								Value: "non_existent_channel",
							},
						},
					},
				},
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Channels: map[string]*discordgo.Channel{
						"non_existent_channel": {ID: "non_existent_channel"},
					},
				},
			},
		},
	}
	handler.HandleFilterCommand(session, staffChannelInteraction)
}

func TestFilterHandler_ButtonActions(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		GuildID:      "guild_1",
		StaffRoleIDs: []string{"role_staff_admin"},
	}

	warningRepo := newMockFilterWarningRepo()
	filterSvc := service.NewWordFilterService(warningRepo, cfg.StaffRoleIDs, silentLogger)
	ticketRepo := newMockTicketRepo()
	ticketSvc := service.NewTicketService(ticketRepo, cfg.StaffRoleIDs, silentLogger)
	handler := handlers.NewFilterHandler(filterSvc, ticketSvc, cfg, nil, silentLogger)

	session := newMockSession(http.StatusOK, `{}`)

	// Pre-seed a violation
	_ = warningRepo.SaveViolation(context.Background(), &domain.FilterViolation{
		GuildID:        "guild_1",
		ChannelID:      "chan_ticket_1",
		TicketID:       "tick_123",
		UserID:         "bad_user_99",
		MessageContent: "very badword",
		MatchedWord:    "badword",
		OriginChannel:  "general_chat",
	})
	ticketRepo.ticketsByChannel["chan_ticket_1"] = &domain.Ticket{
		ID:        "tick_123",
		GuildID:   "guild_1",
		ChannelID: "chan_ticket_1",
		CreatorID: "bad_user_99",
		Type:      domain.TicketTypeFilteredWords,
		Status:    domain.TicketStatusOpen,
	}

	// 1. Staff clicks Warn User
	warnInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_btn_warn",
			Token:     "tok_warn",
			Type:      discordgo.InteractionMessageComponent,
			GuildID:   "guild_1",
			ChannelID: "chan_ticket_1",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "admin_staff_1", Username: "Moderator"},
				Roles: []string{"role_staff_admin"},
			},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "btn_filter_warn",
			},
		},
	}

	handled := handler.HandleComponentInteraction(session, warnInteraction)
	if !handled {
		t.Fatal("expected btn_filter_warn to be handled")
	}

	// Verify warning was saved
	warnings, err := warningRepo.GetWarningsByUserID(context.Background(), "guild_1", "bad_user_99")
	if err != nil || len(warnings) != 1 {
		t.Fatalf("expected 1 warning saved, got %d (err: %v)", len(warnings), err)
	}
	if warnings[0].AdminID != "admin_staff_1" {
		t.Errorf("expected admin ID admin_staff_1, got %s", warnings[0].AdminID)
	}
	if warnings[0].RuleMatched != "badword" {
		t.Errorf("expected rule badword, got %s", warnings[0].RuleMatched)
	}

	// 2. Staff clicks Close without Warning on another ticket
	ticketRepo.ticketsByChannel["chan_ticket_2"] = &domain.Ticket{
		ID:        "tick_456",
		GuildID:   "guild_1",
		ChannelID: "chan_ticket_2",
		CreatorID: "innocent_user",
		Type:      domain.TicketTypeFilteredWords,
		Status:    domain.TicketStatusOpen,
	}

	closeInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_btn_close",
			Token:     "tok_close",
			Type:      discordgo.InteractionMessageComponent,
			GuildID:   "guild_1",
			ChannelID: "chan_ticket_2",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "admin_staff_1"},
				Roles: []string{"role_staff_admin"},
			},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "btn_filter_close_no_warn",
			},
		},
	}

	handled = handler.HandleComponentInteraction(session, closeInteraction)
	if !handled {
		t.Fatal("expected btn_filter_close_no_warn to be handled")
	}

	// Verify NO warning was saved for innocent_user
	innocentWarnings, _ := warningRepo.GetWarningsByUserID(context.Background(), "guild_1", "innocent_user")
	if len(innocentWarnings) != 0 {
		t.Fatalf("expected 0 warnings for innocent_user, got %d", len(innocentWarnings))
	}
}

func TestFilterHandler_HandleWarningsCommand(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		GuildID:      "guild_1",
		StaffRoleIDs: []string{"role_staff_admin"},
	}

	warningRepo := newMockFilterWarningRepo()
	filterSvc := service.NewWordFilterService(warningRepo, cfg.StaffRoleIDs, silentLogger)
	ticketSvc := service.NewTicketService(nil, cfg.StaffRoleIDs, silentLogger)
	handler := handlers.NewFilterHandler(filterSvc, ticketSvc, cfg, nil, silentLogger)

	session := newMockSession(http.StatusOK, `{}`)

	// Pre-seed warning
	_ = warningRepo.SaveWarning(context.Background(), &domain.UserWarning{
		GuildID:        "guild_1",
		UserID:         "warned_user_1",
		AdminID:        "admin_staff_1",
		MessageContent: "bad text snippet",
		RuleMatched:    "bad text",
	})

	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_warns",
			Token:   "tok_warns",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild_1",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "admin_staff_1"},
				Roles: []string{"role_staff_admin"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "user",
						Type:  discordgo.ApplicationCommandOptionUser,
						Value: "warned_user_1",
					},
				},
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Users: map[string]*discordgo.User{
						"warned_user_1": {ID: "warned_user_1", Username: "WarnedPerson"},
					},
				},
			},
		},
	}

	handler.HandleWarningsCommand(session, interaction)
}

func TestFilterHandler_HandleMessageCreate(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		GuildID:      "guild_1",
		StaffRoleIDs: []string{"role_staff_admin"},
	}

	warningRepo := newMockFilterWarningRepo()
	filterSvc := service.NewWordFilterService(warningRepo, cfg.StaffRoleIDs, silentLogger)
	filterSvc.SetGlobalWords([]string{"bannedword"})
	ticketRepo := newMockTicketRepo()
	ticketSvc := service.NewTicketService(ticketRepo, cfg.StaffRoleIDs, silentLogger)
	handler := handlers.NewFilterHandler(filterSvc, ticketSvc, cfg, nil, silentLogger)

	session := newMockSession(http.StatusOK, `{}`)

	// 1. Bot message -> ignored
	handler.HandleMessageCreate(session, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			GuildID:   "guild_1",
			ChannelID: "chan_chat",
			Content:   "bannedword by bot",
			Author: &discordgo.User{
				ID:  "bot_1",
				Bot: true,
			},
		},
	})

	// 2. Clean human message -> ignored
	handler.HandleMessageCreate(session, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			GuildID:   "guild_1",
			ChannelID: "chan_chat",
			Content:   "clean message without issues",
			Author: &discordgo.User{
				ID:  "user_1",
				Bot: false,
			},
		},
	})

	// 3. Violating human message -> detected and deleted
	handler.HandleMessageCreate(session, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg_bad_123",
			GuildID:   "guild_1",
			ChannelID: "chan_chat",
			Content:   "this has bannedword!",
			Author: &discordgo.User{
				ID:       "violator_1",
				Username: "BadUser",
				Bot:      false,
			},
		},
	})
}

