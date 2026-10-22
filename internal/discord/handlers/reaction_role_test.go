package handlers

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/domain"
	"syn/internal/storage"
)

type mockRT func(req *http.Request) (*http.Response, error)

func (m mockRT) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func mockSession() *discordgo.Session {
	s, _ := discordgo.New("Bot mock")
	s.Client.Transport = mockRT(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	return s
}

func TestNormalizeEmojiInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{input: "<:rope:123456789>", want: "rope:123456789"},
		{input: "<a:spinning:987654321>", want: "spinning:987654321"},
		{input: "🪢", want: "🪢"},
		{input: "rope:123456789", want: "rope:123456789"},
		{input: "  <:cuff:111222>  ", want: "cuff:111222"},
	}

	for _, tc := range tests {
		got := NormalizeEmojiInput(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeEmojiInput(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseHexColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{input: "#9B59B6", want: 0x9B59B6, wantErr: false},
		{input: "E91E63", want: 0xE91E63, wantErr: false},
		{input: "  #00FF00  ", want: 0x00FF00, wantErr: false},
		{input: "invalid_hex", want: 0, wantErr: true},
	}

	for _, tc := range tests {
		got, err := parseHexColor(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseHexColor(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseHexColor(%q) = %x, want %x", tc.input, got, tc.want)
		}
	}
}

func TestReactionRoleCommandDefinition(t *testing.T) {
	t.Parallel()

	cmd := ReactionRoleCommandDefinition()
	if cmd.Name != "reaction-panel" {
		t.Fatalf("expected command name 'reaction-panel', got %s", cmd.Name)
	}
	if len(cmd.Options) != 4 {
		t.Fatalf("expected 4 subcommands, got %d", len(cmd.Options))
	}

	subcommandNames := map[string]bool{}
	for _, opt := range cmd.Options {
		subcommandNames[opt.Name] = true
	}

	expectedSubs := []string{"create", "set-image", "add-role", "remove-role"}
	for _, expected := range expectedSubs {
		if !subcommandNames[expected] {
			t.Errorf("missing expected subcommand %q", expected)
		}
	}
}

func TestBuildPanelEmbed(t *testing.T) {
	t.Parallel()

	panel := &domain.ReactionPanel{
		Title:         "Dynamic Roles",
		Intro:         "Select your preferred dynamic roles below.",
		ColorHex:      0x9B59B6,
		BannerFile:    "banner.png",
		ThumbnailFile: "crest.png",
	}

	bindings := []*domain.ReactionRole{
		{
			EmojiDisplay:     "🪢",
			RoleID:           "role_rope",
			Description:      "Rope Bondage",
			RequiresVerified: true,
		},
		{
			EmojiDisplay:     "💜",
			RoleID:           "role_they",
			Description:      "",
			RequiresVerified: false,
		},
	}

	embed := buildPanelEmbed(panel, bindings)
	if embed.Title != panel.Title {
		t.Errorf("expected title %q, got %q", panel.Title, embed.Title)
	}
	if embed.Image == nil || embed.Image.URL != "attachment://banner.png" {
		t.Errorf("expected attachment URL attachment://banner.png, got %v", embed.Image)
	}
	if embed.Thumbnail == nil || embed.Thumbnail.URL != "attachment://crest.png" {
		t.Errorf("expected thumbnail attachment URL attachment://crest.png, got %v", embed.Thumbnail)
	}
	if len(embed.Fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(embed.Fields))
	}
	if !strings.Contains(embed.Fields[0].Value, "*(18+ Verified)*") {
		t.Errorf("expected field to contain 18+ verified badge: %s", embed.Fields[0].Value)
	}

	// Test with no bindings
	emptyEmbed := buildPanelEmbed(panel, nil)
	if !strings.Contains(emptyEmbed.Fields[0].Value, "No roles linked yet") {
		t.Errorf("expected placeholder text when no roles are bound: %s", emptyEmbed.Fields[0].Value)
	}
}

func TestBuildDiscordFiles(t *testing.T) {
	t.Parallel()

	banner := &storage.AssetFile{
		Name:        "banner.png",
		ContentType: "image/png",
		Reader:      bytes.NewReader([]byte("fake_banner")),
	}
	thumb := &storage.AssetFile{
		Name:        "crest.png",
		ContentType: "image/png",
		Reader:      bytes.NewReader([]byte("fake_crest")),
	}

	// 1. Two distinct files
	files := buildDiscordFiles(banner, thumb)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	// 2. Both nil
	nilFiles := buildDiscordFiles(nil, nil)
	if len(nilFiles) != 0 {
		t.Fatalf("expected 0 files, got %d", len(nilFiles))
	}

	// 3. Deduplication: banner and thumb have same filename
	sameThumb := &storage.AssetFile{
		Name:        "banner.png",
		ContentType: "image/png",
		Reader:      bytes.NewReader([]byte("fake_banner")),
	}
	dedupFiles := buildDiscordFiles(banner, sameThumb)
	if len(dedupFiles) != 1 {
		t.Fatalf("expected 1 file after dedup, got %d", len(dedupFiles))
	}
}

func TestReactionRoleHandler_ReactionEvents_IgnoreSelfAndDM(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewReactionRoleHandler(nil, silentLogger)

	session := mockSession()
	session.State.User = &discordgo.User{ID: "bot_id_123"}

	// 1. Reaction from DM (GuildID == "") should be ignored
	handler.HandleReactionAdd(session, &discordgo.MessageReactionAdd{
		MessageReaction: &discordgo.MessageReaction{
			GuildID: "",
			UserID:  "some_user",
		},
	})

	// 2. Reaction from Bot itself should be ignored
	handler.HandleReactionAdd(session, &discordgo.MessageReactionAdd{
		MessageReaction: &discordgo.MessageReaction{
			GuildID: "guild_1",
			UserID:  "bot_id_123",
		},
	})

	// 3. Reaction Remove from DM should be ignored
	handler.HandleReactionRemove(session, &discordgo.MessageReactionRemove{
		MessageReaction: &discordgo.MessageReaction{
			GuildID: "",
			UserID:  "some_user",
		},
	})

	// 4. Reaction Remove from Bot itself should be ignored
	handler.HandleReactionRemove(session, &discordgo.MessageReactionRemove{
		MessageReaction: &discordgo.MessageReaction{
			GuildID: "guild_1",
			UserID:  "bot_id_123",
		},
	})
}

func TestReactionRoleHandler_HandlePanelCommand_Routing(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewReactionRoleHandler(nil, silentLogger)
	session := mockSession()

	// 1. Empty subcommands
	handler.HandlePanelCommand(session, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{},
			},
		},
	})

	// 2. Unknown subcommand
	handler.HandlePanelCommand(session, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "unknown"},
				},
			},
		},
	})
}

func TestReactionRoleHandler_CheckChannelPermissions(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewReactionRoleHandler(nil, silentLogger)
	session := mockSession()

	// When state user is nil, returns nil
	missing := handler.checkChannelPermissions(session, "ch_1")
	if missing != nil {
		t.Errorf("expected nil missing permissions when state user is nil, got %v", missing)
	}
}

func TestReactionRoleHandler_CheckRoleHierarchy(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewReactionRoleHandler(nil, silentLogger)
	session := mockSession()

	// When state user is nil, returns ok
	ok, _ := handler.checkRoleHierarchy(session, "guild_1", "role_1")
	if !ok {
		t.Errorf("expected ok when state is uninitialized")
	}
}
