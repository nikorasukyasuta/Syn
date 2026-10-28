package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/handlers"
	"syn/internal/discord/syncutil"
	"syn/internal/domain"
	"syn/internal/service"
)

type mockIDVerifyRepoHandler struct {
	mu       sync.Mutex
	requests map[string]*domain.IDVerificationRequest
}

func newMockIDVerifyRepoHandler() *mockIDVerifyRepoHandler {
	return &mockIDVerifyRepoHandler{
		requests: make(map[string]*domain.IDVerificationRequest),
	}
}

func (m *mockIDVerifyRepoHandler) CreateRequest(_ context.Context, req *domain.IDVerificationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	req.ID = "id-req-" + req.ChannelID
	m.requests[req.ID] = req
	return nil
}

func (m *mockIDVerifyRepoHandler) GetRequestByChannelID(_ context.Context, guildID, channelID string) (*domain.IDVerificationRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if r.GuildID == guildID && r.ChannelID == channelID {
			return r, nil
		}
	}
	return nil, domain.ErrIDVerifyRequestNotFound
}

func (m *mockIDVerifyRepoHandler) GetPendingRequestByUser(_ context.Context, guildID, userID string) (*domain.IDVerificationRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if r.GuildID == guildID && r.UserID == userID && r.Status == domain.IDVerificationStatusPending {
			return r, nil
		}
	}
	return nil, nil
}

func (m *mockIDVerifyRepoHandler) UpdateRequestStatus(
	_ context.Context,
	id string,
	status domain.IDVerificationStatus,
	reviewerID, reason string,
	reviewedAt time.Time,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.requests[id]
	if !ok {
		return domain.ErrIDVerifyRequestNotFound
	}
	r.Status = status
	r.ReviewedByID = reviewerID
	r.ReviewReason = reason
	r.ReviewedAt = &reviewedAt
	return nil
}

type mockVerifyRepoStateful struct {
	mu      sync.Mutex
	records map[string]*domain.Verification
}

func newMockVerifyRepoStateful() *mockVerifyRepoStateful {
	return &mockVerifyRepoStateful{
		records: make(map[string]*domain.Verification),
	}
}

func (m *mockVerifyRepoStateful) GetByUserID(_ context.Context, guildID, userID string) (*domain.Verification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[guildID+":"+userID], nil
}

func (m *mockVerifyRepoStateful) SaveVerification(_ context.Context, v *domain.Verification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[v.GuildID+":"+v.DiscordUserID] = v
	return nil
}

func TestIDVerifyCommandDefinition(t *testing.T) {
	t.Parallel()

	cmd := handlers.IDVerifyCommandDefinition()
	if cmd.Name != "id-verify" {
		t.Fatalf("expected command name 'id-verify', got %s", cmd.Name)
	}
	if cmd.DefaultMemberPermissions == nil || *cmd.DefaultMemberPermissions != 0 {
		t.Fatal("expected DefaultMemberPermissions to be 0 (Staff-gated)")
	}
	if cmd.DMPermission == nil || *cmd.DMPermission != false {
		t.Fatal("expected DMPermission to be false")
	}

	expectedSubs := map[string]bool{
		"panel":   false,
		"approve": false,
		"deny":    false,
		"revoke":  false,
		"status":  false,
	}
	for _, opt := range cmd.Options {
		if _, ok := expectedSubs[opt.Name]; ok {
			expectedSubs[opt.Name] = true
		}
	}
	for name, found := range expectedSubs {
		if !found {
			t.Errorf("expected subcommand %q in /id-verify", name)
		}
	}
}

func TestBuildIDVerifyPanel(t *testing.T) {
	t.Parallel()

	embed := handlers.BuildIDVerifyPanelEmbed()
	if embed == nil || embed.Title == "" {
		t.Fatal("expected non-empty ID verify panel embed")
	}

	buttons := handlers.BuildIDVerifyPanelButtons()
	if len(buttons) != 1 {
		t.Fatalf("expected 1 action row for ID verify panel, got %d", len(buttons))
	}
}

func TestIDVerifyHandler_FullFlowAndStaffGate(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	idRepo := newMockIDVerifyRepoHandler()
	vRepo := newMockVerifyRepoStateful()
	cfg := &config.Config{
		GuildID:          "guild_1",
		VerifiedRoleID:   "role_verified",
		IDVerifiedRoleID: "role_id_verified",
		UnverifiedRoleID: "role_unverified",
		StaffRoleIDs:     []string{"role_staff"},
	}
	lockMgr := syncutil.NewUserLockManager()
	svc := service.NewIDVerificationService(idRepo, vRepo, nil, cfg.StaffRoleIDs, logger)
	h := handlers.NewIDVerifyHandler(svc, cfg, nil, lockMgr, logger)

	var roleMu sync.Mutex
	grantedRoles := make(map[string]bool)
	session, _ := discordgo.New("Bot test_token")
	session.StateEnabled = true
	session.State.User = &discordgo.User{ID: "bot_id"}
	session.Client = &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/members/applicant_1/roles/") {
				parts := strings.Split(req.URL.Path, "/roles/")
				if len(parts) == 2 {
					roleMu.Lock()
					grantedRoles[parts[1]] = true
					roleMu.Unlock()
				}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id": "ch_id_verify_100"}`)),
			}, nil
		}),
	}

	// 1. Non-staff member attempting to invoke /id-verify panel is blocked
	unauthCmd := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_unauth",
			GuildID: "guild_1",
			Type:    discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "regular_user"},
				Roles: []string{"role_regular"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "id-verify",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "panel",
						Type: discordgo.ApplicationCommandOptionSubCommand,
					},
				},
			},
		},
	}
	h.HandleIDVerifyCommand(session, unauthCmd)

	// 2. Staff posts /id-verify panel
	panelCmd := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_panel",
			GuildID: "guild_1",
			Type:    discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_user"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "id-verify",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "panel",
						Type: discordgo.ApplicationCommandOptionSubCommand,
					},
				},
			},
		},
	}
	h.HandleIDVerifyCommand(session, panelCmd)

	// 3. Regular user clicks "btn_id_verify_start" to open their private ID verification channel
	startClick := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_start",
			GuildID: "guild_1",
			Type:    discordgo.InteractionMessageComponent,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "applicant_1", Username: "applicant"},
				Roles: []string{"role_unverified"},
			},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "btn_id_verify_start",
			},
		},
	}
	if !h.HandleComponentInteraction(session, startClick) {
		t.Fatal("expected HandleComponentInteraction to handle btn_id_verify_start")
	}

	req, err := idRepo.GetPendingRequestByUser(context.Background(), "guild_1", "applicant_1")
	if err != nil || req == nil {
		t.Fatalf("expected pending ID verification request for applicant_1, got req=%v err=%v", req, err)
	}

	// 4. Non-staff user tries to click "btn_id_verify_approve:applicant_1" -> blocked!
	unauthApproveClick := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_unauth_btn",
			GuildID:   "guild_1",
			ChannelID: "ch_id_verify_100",
			Type:      discordgo.InteractionMessageComponent,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "applicant_1"},
				Roles: []string{"role_unverified"},
			},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "btn_id_verify_approve:applicant_1",
			},
		},
	}
	h.HandleComponentInteraction(session, unauthApproveClick)

	verBefore, _ := vRepo.GetByUserID(context.Background(), "guild_1", "applicant_1")
	if verBefore != nil && verBefore.IsVerified {
		t.Fatal("expected applicant_1 to NOT be verified when non-staff clicks approve")
	}

	// 5. Staff runs /id-verify approve inside the channel
	approveCmd := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_approve",
			GuildID:   "guild_1",
			ChannelID: "ch_id_verify_100",
			Type:      discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_user"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "id-verify",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "approve",
						Type: discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{
							{
								Name:  "notes",
								Type:  discordgo.ApplicationCommandOptionString,
								Value: "DOB verified",
							},
						},
					},
				},
			},
		},
	}
	h.HandleIDVerifyCommand(session, approveCmd)

	verAfter, _ := vRepo.GetByUserID(context.Background(), "guild_1", "applicant_1")
	if verAfter == nil || !verAfter.IsVerified {
		t.Fatal("expected applicant_1 to be verified after staff approval")
	}

	roleMu.Lock()
	gotVerified := grantedRoles["role_verified"]
	gotIDVerified := grantedRoles["role_id_verified"]
	roleMu.Unlock()
	if !gotVerified || !gotIDVerified {
		t.Fatalf("expected both role_verified (%v) and role_id_verified (%v) to be granted upon ID verification approval", gotVerified, gotIDVerified)
	}

	// 6. Staff runs /id-verify status for applicant_1
	statusCmd := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_status",
			GuildID: "guild_1",
			Type:    discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_user"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "id-verify",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "status",
						Type: discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{
							{
								Name:  "member",
								Type:  discordgo.ApplicationCommandOptionUser,
								Value: "applicant_1",
							},
						},
					},
				},
			},
		},
	}
	h.HandleIDVerifyCommand(session, statusCmd)

	// 7. Staff runs /id-verify revoke for applicant_1
	revokeCmd := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_revoke",
			GuildID: "guild_1",
			Type:    discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "staff_user"},
				Roles: []string{"role_staff"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "id-verify",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name: "revoke",
						Type: discordgo.ApplicationCommandOptionSubCommand,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{
							{
								Name:  "member",
								Type:  discordgo.ApplicationCommandOptionUser,
								Value: "applicant_1",
							},
							{
								Name:  "reason",
								Type:  discordgo.ApplicationCommandOptionString,
								Value: "Policy violation",
							},
						},
					},
				},
			},
		},
	}
	h.HandleIDVerifyCommand(session, revokeCmd)

	verRevoked, _ := vRepo.GetByUserID(context.Background(), "guild_1", "applicant_1")
	if verRevoked == nil || verRevoked.IsVerified {
		t.Fatal("expected applicant_1 verification to be revoked")
	}

	// 8. Member who already holds the dedicated IDVerifiedRoleID is blocked from opening a duplicate session
	alreadyIDVerifiedClick := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "int_already_idv",
			GuildID: "guild_1",
			Type:    discordgo.InteractionMessageComponent,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "applicant_idv", Username: "alreadyidv"},
				Roles: []string{"role_verified", "role_id_verified"},
			},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "btn_id_verify_start",
			},
		},
	}
	h.HandleComponentInteraction(session, alreadyIDVerifiedClick)
	if dupReq, _ := idRepo.GetPendingRequestByUser(context.Background(), "guild_1", "applicant_idv"); dupReq != nil {
		t.Fatalf("expected member already holding role_id_verified to not create a new request, got %+v", dupReq)
	}
}

