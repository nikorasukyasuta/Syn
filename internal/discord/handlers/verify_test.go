package handlers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/discord/handlers"
	"syn/internal/domain"
	"syn/internal/service"
)

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func newMockSession(statusCode int, respBody string) *discordgo.Session {
	s, _ := discordgo.New("Bot mock_token")
	s.Client.Transport = mockRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(respBody)),
			Header:     make(http.Header),
		}, nil
	})
	return s
}

type mockVerificationRepo struct {
	existing *domain.Verification
	saveErr  error
}

func (m *mockVerificationRepo) GetByUserID(_ context.Context, _, _ string) (*domain.Verification, error) {
	return m.existing, nil
}

func (m *mockVerificationRepo) SaveVerification(_ context.Context, _ *domain.Verification) error {
	return m.saveErr
}

func (m *mockVerificationRepo) LogAudit(_ context.Context, _, _, _ string) error {
	return nil
}

func TestVerifyCommandDefinition(t *testing.T) {
	t.Parallel()

	cmd := handlers.VerifyCommandDefinition()
	if cmd.Name != "verify-member" {
		t.Fatalf("expected command name 'verify-member', got %s", cmd.Name)
	}
	if cmd.DMPermission == nil || *cmd.DMPermission != false {
		t.Fatal("expected DMPermission to be false")
	}
	if len(cmd.Options) != 1 || cmd.Options[0].Name != "member" {
		t.Fatal("expected single 'member' option")
	}
}

func TestVerifyHandler_HandleVerifyCommand_NoOptions(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	vRepo := &mockVerificationRepo{}
	vSvc := service.NewVerificationService(vRepo, []string{"role_staff"}, silentLogger)
	h := handlers.NewVerifyHandler(vSvc, "role_verified", "role_unverified")

	session := newMockSession(http.StatusOK, `{}`)
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_1",
			Token: "tok_1",
			Type:  discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{},
			},
		},
	}

	h.HandleVerifyCommand(session, interaction)
}

func TestVerifyHandler_HandleVerifyCommand_Success(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	vRepo := &mockVerificationRepo{}
	vSvc := service.NewVerificationService(vRepo, []string{"role_staff"}, silentLogger)
	h := handlers.NewVerifyHandler(vSvc, "role_verified", "role_unverified")

	session := newMockSession(http.StatusOK, `{}`)
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_2",
			Token:   "tok_2",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild_1",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_1"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "member",
						Type:  discordgo.ApplicationCommandOptionUser,
						Value: "user_target_999",
					},
				},
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Users: map[string]*discordgo.User{
						"user_target_999": {ID: "user_target_999"},
					},
				},
			},
		},
	}

	h.HandleVerifyCommand(session, interaction)
}

func TestVerifyHandler_HandleVerifyCommand_ServiceError(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	vRepo := &mockVerificationRepo{saveErr: errors.New("db error")}
	vSvc := service.NewVerificationService(vRepo, []string{"role_staff"}, silentLogger)
	h := handlers.NewVerifyHandler(vSvc, "role_verified", "role_unverified")

	session := newMockSession(http.StatusOK, `{}`)
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_3",
			Token:   "tok_3",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild_1",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_1"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "member",
						Type:  discordgo.ApplicationCommandOptionUser,
						Value: "user_target_999",
					},
				},
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Users: map[string]*discordgo.User{
						"user_target_999": {ID: "user_target_999"},
					},
				},
			},
		},
	}

	h.HandleVerifyCommand(session, interaction)
}

func TestVerifyHandler_HandleVerifyCommand_ReplacesUnverifiedRole(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	vRepo := &mockVerificationRepo{}
	vSvc := service.NewVerificationService(vRepo, []string{"role_staff"}, silentLogger)
	h := handlers.NewVerifyHandler(vSvc, "role_verified", "role_unverified", silentLogger)

	session := newMockSession(http.StatusOK, `{}`)
	// Member has role_unverified in i.Member.Roles
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_4",
			Token:   "tok_4",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild_1",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "target_user_1"},
				Roles: []string{"role_staff", "role_unverified"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "member",
						Type:  discordgo.ApplicationCommandOptionUser,
						Value: "target_user_1",
					},
				},
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Users: map[string]*discordgo.User{
						"target_user_1": {ID: "target_user_1"},
					},
				},
			},
		},
	}

	h.HandleVerifyCommand(session, interaction)
}

func TestVerifyHandler_HandleVerifyCommand_AlreadyVerifiedSyncsRoles(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	vRepo := &mockVerificationRepo{
		existing: &domain.Verification{
			DiscordUserID: "target_user_2",
			GuildID:       "guild_1",
			IsVerified:    true,
		},
	}
	vSvc := service.NewVerificationService(vRepo, []string{"role_staff"}, silentLogger)
	h := handlers.NewVerifyHandler(vSvc, "role_verified", "role_unverified", silentLogger)

	session := newMockSession(http.StatusOK, `{}`)
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_5",
			Token:   "tok_5",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: "guild_1",
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_1"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "member",
						Type:  discordgo.ApplicationCommandOptionUser,
						Value: "target_user_2",
					},
				},
				Resolved: &discordgo.ApplicationCommandInteractionDataResolved{
					Users: map[string]*discordgo.User{
						"target_user_2": {ID: "target_user_2"},
					},
				},
			},
		},
	}

	h.HandleVerifyCommand(session, interaction)
}

