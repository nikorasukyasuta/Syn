package handlers_test

import (
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/handlers"
)

func TestLegalCommandDefinitions(t *testing.T) {
	termsCmd := handlers.TermsCommandDefinition()
	if termsCmd.Name != "terms" {
		t.Fatalf("expected command name 'terms', got %s", termsCmd.Name)
	}

	privacyCmd := handlers.PrivacyCommandDefinition()
	if privacyCmd.Name != "privacy" {
		t.Fatalf("expected command name 'privacy', got %s", privacyCmd.Name)
	}
}

func TestLegalHandler_HandleTermsCommand(t *testing.T) {
	cfg := &config.Config{
		WebBaseURL: "https://syn.example.com",
	}
	h := handlers.NewLegalHandler(cfg)

	session := newMockSession(http.StatusOK, `{}`)
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_terms",
			ChannelID: "ch_general",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "terms",
			},
		},
	}

	h.HandleTermsCommand(session, interaction)
}

func TestLegalHandler_HandlePrivacyCommand(t *testing.T) {
	cfg := &config.Config{
		WebBaseURL: "https://syn.example.com",
	}
	h := handlers.NewLegalHandler(cfg)

	session := newMockSession(http.StatusOK, `{}`)
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_privacy",
			ChannelID: "ch_general",
			Type:      discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "privacy",
			},
		},
	}

	h.HandlePrivacyCommand(session, interaction)
}

