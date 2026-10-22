package discord

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/cache"
	"syn/internal/discord/handlers"
	"syn/internal/discord/queue"
	"syn/internal/discord/syncutil"
	"syn/internal/service"
	"sync"
)

func TestNewBot_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DiscordToken:     "mock_token",
		GuildID:          "guild_123",
		VerifiedRoleID:   "role_verified_123",
		UnverifiedRoleID: "role_unverified_123",
		StaffRoleIDs:     []string{"role_staff_1"},
	}
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	lockMgr := syncutil.NewUserLockManager()
	verifyHandler := handlers.NewVerifyHandler(nil, cfg.VerifiedRoleID, cfg.UnverifiedRoleID, lockMgr)
	rrHandler := handlers.NewReactionRoleHandler(nil, silentLogger)
	memberHandler := handlers.NewMemberJoinHandler(cfg.GuildID, cfg.UnverifiedRoleID, silentLogger)
	resCache := cache.NewResourceCache(silentLogger)
	safewordHandler := handlers.NewSafewordHandler(cfg, nil, silentLogger)
	healthHandler := handlers.NewHealthHandler(cfg.StaffRoleIDs, nil, nil, lockMgr)
	versionHandler := handlers.NewVersionHandler()
	filterSvc := service.NewWordFilterService(nil, cfg.StaffRoleIDs, silentLogger)
	ticketSvc := service.NewTicketService(nil, cfg.StaffRoleIDs, silentLogger)
	filterHandler := handlers.NewFilterHandler(filterSvc, ticketSvc, cfg, resCache, silentLogger)
	flashSvc := service.NewFlashDashService(nil, cfg.StaffRoleIDs, silentLogger)
	flashHandler := handlers.NewFlashHandler(flashSvc, cfg, silentLogger)
	legalHandler := handlers.NewLegalHandler(cfg)

	bot, err := NewBot(
		cfg,
		silentLogger,
		verifyHandler,
		rrHandler,
		memberHandler,
		safewordHandler,
		nil,
		healthHandler,
		versionHandler,
		filterHandler,
		flashHandler,
		legalHandler,
		nil,
		lockMgr,
		resCache,
	)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}
	if bot == nil {
		t.Fatal("expected non-nil bot instance")
	}

	if bot.Cache() != resCache {
		t.Fatal("expected bot.Cache() to match provided cache")
	}

	// Verify routes registered
	if _, ok := bot.routes["health"]; !ok {
		t.Error("expected /health to be registered in bot.routes")
	}
	if _, ok := bot.routes["version"]; !ok {
		t.Error("expected /version to be registered in bot.routes")
	}
	if _, ok := bot.routes["filter"]; !ok {
		t.Error("expected /filter to be registered in bot.routes")
	}
	if _, ok := bot.routes["warnings"]; !ok {
		t.Error("expected /warnings to be registered in bot.routes")
	}
	if _, ok := bot.routes["flash"]; !ok {
		t.Error("expected /flash to be registered in bot.routes")
	}
	if _, ok := bot.routes["terms"]; !ok {
		t.Error("expected /terms to be registered in bot.routes")
	}
	if _, ok := bot.routes["privacy"]; !ok {
		t.Error("expected /privacy to be registered in bot.routes")
	}
	if bot.Session() == nil {
		t.Error("expected bot.Session() to return non-nil session")
	}

	// Test RegisterCommand
	cmd := &discordgo.ApplicationCommand{
		Name:        "custom-test",
		Description: "A test command",
	}
	called := false
	bot.RegisterCommand(cmd, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		called = true
	})

	// Test routing of registered command
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "custom-test",
			},
		},
	}
	bot.handleInteraction(bot.session, interaction)
	if !called {
		t.Fatal("expected registered command handler to be invoked")
	}

	// Test unhandled command
	unhandledInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "non-existent-cmd",
			},
		},
	}
	bot.handleInteraction(bot.session, unhandledInteraction)

	// Test nil interaction
	bot.handleInteraction(bot.session, nil)
	bot.handleInteraction(bot.session, &discordgo.InteractionCreate{})

	// Test Component interaction logging
	bot.handleInteraction(bot.session, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID:      "test_button",
				ComponentType: discordgo.ButtonComponent,
			},
		},
	})

	// Test Modal Submit interaction logging
	bot.handleInteraction(bot.session, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionModalSubmit,
			Data: discordgo.ModalSubmitInteractionData{
				CustomID: "test_modal",
			},
		},
	})
}

func TestBot_ResolveAppID(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DiscordToken:     "mock_token",
		GuildID:          "guild_123",
		VerifiedRoleID:   "role_verified_123",
		UnverifiedRoleID: "role_unverified_123",
		StaffRoleIDs:     []string{"role_staff_1"},
	}
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	bot, err := NewBot(cfg, silentLogger, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	// 1. When session.State.User is populated
	bot.session.State.User = &discordgo.User{ID: "app_id_999", Username: "TestBot"}
	appID, err := bot.resolveAppID()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if appID != "app_id_999" {
		t.Fatalf("expected app_id_999, got: %s", appID)
	}

	// 2. When session.State is nil
	bot.session.State = nil
	// Calling resolveAppID will attempt session.User("@me") which will fail with mock_token,
	// verifying that it does not panic with nil pointer dereference!
	_, err = bot.resolveAppID()
	if err == nil {
		t.Fatal("expected error with mock token, got nil")
	}
}

func TestBot_TicketInteractionRouting(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DiscordToken:     "mock_token",
		GuildID:          "guild_123",
		VerifiedRoleID:   "role_verified_123",
		UnverifiedRoleID: "role_unverified_123",
		StaffRoleIDs:     []string{"role_staff_1"},
	}
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc := handlers.NewTicketHandler(nil, cfg, nil, silentLogger)

	bot, err := NewBot(cfg, silentLogger, nil, nil, nil, nil, svc, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	// Verify /ticket was registered
	if _, exists := bot.routes["ticket"]; !exists {
		t.Fatal("expected /ticket command to be registered in bot.routes")
	}

	// Verify Component Interaction routing does not panic
	compInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "btn_ticket_create:invalid",
			},
		},
	}
	bot.handleInteraction(bot.session, compInteraction)

	// Verify Modal Submit interaction routing does not panic
	modalInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionModalSubmit,
			Data: discordgo.ModalSubmitInteractionData{
				CustomID: "modal_ticket_close",
			},
		},
	}
	bot.handleInteraction(bot.session, modalInteraction)
}

func TestBot_QueueInteractionRouting(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DiscordToken:     "mock_token",
		GuildID:          "guild_123",
		VerifiedRoleID:   "role_verified_123",
		UnverifiedRoleID: "role_unverified_123",
		StaffRoleIDs:     []string{"role_staff_1"},
	}
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cmdQueue := queue.NewCommandQueue(50, 2, silentLogger)
	defer cmdQueue.Stop(time.Second)

	bot, err := NewBot(cfg, silentLogger, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, cmdQueue, nil, nil)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)

	bot.RegisterCommand(&discordgo.ApplicationCommand{Name: "queued-cmd"}, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		wg.Done()
	})

	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:   "int_queued",
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "queued-cmd",
			},
		},
	}

	bot.handleInteraction(bot.session, interaction)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Succeeded in routing through worker queue
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for queued command to execute")
	}
}
