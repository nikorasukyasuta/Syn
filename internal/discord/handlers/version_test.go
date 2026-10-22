package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/discord/handlers"
	"syn/internal/version"
)

func TestVersionCommandDefinition(t *testing.T) {
	t.Parallel()

	cmd := handlers.VersionCommandDefinition()
	if cmd.Name != "version" {
		t.Fatalf("expected command name 'version', got %s", cmd.Name)
	}
	if cmd.DMPermission == nil || *cmd.DMPermission != false {
		t.Fatal("expected DMPermission to be false")
	}
}

func TestBuildVersionEmbed(t *testing.T) {
	t.Parallel()

	embed := handlers.BuildVersionEmbed()
	if embed == nil {
		t.Fatal("expected non-nil version embed")
	}

	foundVersion := false
	foundRepo := false
	for _, f := range embed.Fields {
		if strings.Contains(f.Value, version.CurrentVersion) {
			foundVersion = true
		}
		if strings.Contains(f.Value, version.GitRepositoryURL) {
			foundRepo = true
		}
	}

	if !foundVersion {
		t.Errorf("expected embed to contain version %s", version.CurrentVersion)
	}
	if !foundRepo {
		t.Errorf("expected embed to contain GitHub URL %s", version.GitRepositoryURL)
	}
}

func TestVersionHandler_HandleVersionCommand(t *testing.T) {
	t.Parallel()

	h := handlers.NewVersionHandler()
	session := newMockSession(http.StatusOK, `{}`)

	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_ver",
			Token: "tok_ver",
			Type:  discordgo.InteractionApplicationCommand,
		},
	}

	h.HandleVersionCommand(session, interaction)
}

