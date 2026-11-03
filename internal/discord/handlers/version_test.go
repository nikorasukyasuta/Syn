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

func TestVersionHandler_Pagination(t *testing.T) {
	t.Parallel()

	h := handlers.NewVersionHandler()
	session := newMockSession(http.StatusOK, `{}`)

	// Verify BuildVersionPageEmbed renders older releases and clamps out-of-range pages
	for idx, rel := range version.History {
		embed := handlers.BuildVersionPageEmbed(idx)
		if embed == nil {
			t.Fatalf("expected non-nil embed for page %d", idx)
		}
		if !strings.Contains(embed.Footer.Text, rel.Version) {
			t.Errorf("expected footer to contain release version %s on page %d, got %s", rel.Version, idx, embed.Footer.Text)
		}
	}

	components := handlers.BuildVersionComponents(0)
	if len(components) != 2 {
		t.Fatalf("expected 2 component rows (navigation buttons + version jump select menu), got %d", len(components))
	}

	// Next button click
	nextInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:   "int_ver_next",
			Type: discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "btn_version_next:1",
			},
		},
	}
	if !h.HandleComponentInteraction(session, nextInteraction) {
		t.Fatal("expected HandleComponentInteraction to handle btn_version_next:1")
	}

	// Select menu jump to oldest version
	selectInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:   "int_ver_select",
			Type: discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "select_version_page",
				Values:   []string{"3"},
			},
		},
	}
	if !h.HandleComponentInteraction(session, selectInteraction) {
		t.Fatal("expected HandleComponentInteraction to handle select_version_page")
	}

	// Unrelated component ID returns false
	unrelatedInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:   "int_unrelated",
			Type: discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "btn_other_feature",
			},
		},
	}
	if h.HandleComponentInteraction(session, unrelatedInteraction) {
		t.Fatal("expected HandleComponentInteraction to ignore unrelated component custom ID")
	}
}

func TestBuildUpdateAnnouncementEmbed(t *testing.T) {
	t.Parallel()

	embed := handlers.BuildUpdateAnnouncementEmbed()
	if embed == nil {
		t.Fatal("expected non-nil update announcement embed")
	}
	if !strings.Contains(embed.Title, version.CurrentVersion) {
		t.Errorf("expected announcement title to include %s, got %q", version.CurrentVersion, embed.Title)
	}
	if !strings.Contains(embed.Description, version.CurrentVersion) {
		t.Errorf("expected announcement description to include %s", version.CurrentVersion)
	}
}

