package discord_test

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/discord"
)

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func newMockSession() *discordgo.Session {
	s, _ := discordgo.New("Bot mock_token")
	s.Client.Transport = mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	return s
}

func TestHasAnyRole(t *testing.T) {
	t.Parallel()

	allowedSet := map[string]struct{}{
		"role_staff_1":  {},
		"role_helper_2": {},
	}

	tests := []struct {
		name        string
		memberRoles []string
		want        bool
	}{
		{
			name:        "Authorized: Member has Staff role",
			memberRoles: []string{"role_verified", "role_staff_1"},
			want:        true,
		},
		{
			name:        "Authorized: Member has Server Helper role",
			memberRoles: []string{"role_helper_2"},
			want:        true,
		},
		{
			name:        "Unauthorized: Member only has regular roles",
			memberRoles: []string{"role_verified", "role_kinkster"},
			want:        false,
		},
		{
			name:        "Unauthorized: Member has no roles",
			memberRoles: []string{},
			want:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := discord.HasAnyRole(tc.memberRoles, allowedSet)
			if got != tc.want {
				t.Fatalf("HasAnyRole() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRequireRoles_Authorized(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	session := newMockSession()
	called := false

	handler := discord.RequireRoles([]string{"role_staff"}, silentLogger, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		called = true
	})

	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID: "guild_123",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "user_1"},
				Roles: []string{"role_staff"},
			},
		},
	}

	handler(session, interaction)
	if !called {
		t.Fatal("expected handler to be invoked for authorized member")
	}
}

func TestRequireRoles_Unauthorized(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	session := newMockSession()
	called := false

	handler := discord.RequireRoles([]string{"role_staff"}, silentLogger, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		called = true
	})

	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_1",
			Token:   "tok_1",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild_123",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "user_2"},
				Roles: []string{"role_regular"},
			},
		},
	}

	handler(session, interaction)
	if called {
		t.Fatal("expected handler NOT to be invoked for unauthorized member")
	}
}

func TestRequireRoles_AutocompleteResponse(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	session := newMockSession()
	called := false

	handler := discord.RequireRoles([]string{"role_staff"}, silentLogger, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		called = true
	})

	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_2",
			Token:   "tok_2",
			Type:    discordgo.InteractionApplicationCommandAutocomplete,
			GuildID: "guild_123",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "user_3"},
				Roles: []string{"role_regular"},
			},
		},
	}

	handler(session, interaction)
	if called {
		t.Fatal("expected handler NOT to be invoked for autocomplete by unauthorized member")
	}
}

func TestRecoverPanic(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	session := newMockSession()

	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_3",
			Token:   "tok_3",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild_123",
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "user_4"},
			},
		},
	}

	handler := discord.RecoverPanic(silentLogger, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		panic("deliberate panic in test handler")
	})

	// Must not bubble panic to caller
	handler(session, interaction)
}
