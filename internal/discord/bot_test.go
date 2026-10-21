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
}

