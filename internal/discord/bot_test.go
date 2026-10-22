package discord

import (
	"io"
	"log/slog"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/cache"
	"syn/internal/discord/handlers"
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

	verifyHandler := handlers.NewVerifyHandler(nil, cfg.VerifiedRoleID, cfg.UnverifiedRoleID)
	rrHandler := handlers.NewReactionRoleHandler(nil, silentLogger)
	memberHandler := handlers.NewMemberJoinHandler(cfg.GuildID, cfg.UnverifiedRoleID, silentLogger)
	resCache := cache.NewResourceCache(silentLogger)

	bot, err := NewBot(cfg, silentLogger, verifyHandler, rrHandler, memberHandler, resCache)
	if err != nil {
		t.Fatalf("failed to create bot: %v", err)
	}
	if bot == nil {
		t.Fatal("expected non-nil bot instance")
	}

	if bot.Cache() != resCache {
		t.Fatal("expected bot.Cache() to match provided cache")
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
	bot, err := NewBot(cfg, silentLogger, nil, nil, nil, nil)
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


