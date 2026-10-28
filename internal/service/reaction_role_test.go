package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"syn/internal/domain"
	"syn/internal/service"
	"syn/internal/storage"
)

type mockReactionRoleRepo struct {
	mapping *domain.ReactionRole
	panel   *domain.ReactionPanel
}

func (m *mockReactionRoleRepo) UpsertMapping(_ context.Context, rr *domain.ReactionRole) error {
	m.mapping = rr
	return nil
}

func (m *mockReactionRoleRepo) GetMapping(_ context.Context, _, _, _ string) (*domain.ReactionRole, error) {
	return m.mapping, nil
}

func (m *mockReactionRoleRepo) ListByMessage(_ context.Context, _, _ string) ([]*domain.ReactionRole, error) {
	if m.mapping != nil {
		return []*domain.ReactionRole{m.mapping}, nil
	}
	return nil, nil
}

func (m *mockReactionRoleRepo) ListByGuild(_ context.Context, _ string) ([]*domain.ReactionRole, error) {
	if m.mapping != nil {
		return []*domain.ReactionRole{m.mapping}, nil
	}
	return nil, nil
}

func (m *mockReactionRoleRepo) DeleteMapping(_ context.Context, _, _, _ string) error {
	m.mapping = nil
	return nil
}

func (m *mockReactionRoleRepo) SavePanel(_ context.Context, panel *domain.ReactionPanel) error {
	m.panel = panel
	return nil
}

func (m *mockReactionRoleRepo) GetPanel(_ context.Context, _, _ string) (*domain.ReactionPanel, error) {
	if m.panel != nil {
		return m.panel, nil
	}
	return nil, domain.ErrPanelNotFound
}

func (m *mockReactionRoleRepo) DeletePanel(_ context.Context, _, _ string) error {
	m.panel = nil
	return nil
}

func TestReactionRoleService_CreateBinding(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	staffRoles := []string{"role_staff_1", "role_helper_2"}
	verifiedRole := "role_18_plus_verified"

	tests := []struct {
		name       string
		actorID    string
		actorRoles []string
		rr         *domain.ReactionRole
		wantErr    error
	}{
		{
			name:       "Success: Server Helper binds a valid kink role",
			actorID:    "helper_1",
			actorRoles: []string{"role_helper_2"},
			rr: &domain.ReactionRole{
				GuildID:          "guild_1",
				ChannelID:        "chan_1",
				MessageID:        "msg_100",
				EmojiAPIName:     "🪢",
				RoleID:           "role_rigger",
				RequiresVerified: true,
			},
			wantErr: nil,
		},
		{
			name:       "Security Block: Staff tries to bind Staff role to a reaction (Privilege Escalation)",
			actorID:    "staff_1",
			actorRoles: []string{"role_staff_1"},
			rr: &domain.ReactionRole{
				GuildID:      "guild_1",
				ChannelID:    "chan_1",
				MessageID:    "msg_100",
				EmojiAPIName: "👑",
				RoleID:       "role_staff_1",
			},
			wantErr: domain.ErrProtectedRole,
		},
		{
			name:       "Security Block: Helper tries to bind 18+ Verified role to a reaction",
			actorID:    "helper_1",
			actorRoles: []string{"role_helper_2"},
			rr: &domain.ReactionRole{
				GuildID:      "guild_1",
				ChannelID:    "chan_1",
				MessageID:    "msg_100",
				EmojiAPIName: "✅",
				RoleID:       verifiedRole,
			},
			wantErr: domain.ErrProtectedRole,
		},
		{
			name:       "RBAC Block: Regular member attempts to create a reaction role",
			actorID:    "member_1",
			actorRoles: []string{verifiedRole},
			rr: &domain.ReactionRole{
				GuildID:      "guild_1",
				ChannelID:    "chan_1",
				MessageID:    "msg_100",
				EmojiAPIName: "🪢",
				RoleID:       "role_rigger",
			},
			wantErr: domain.ErrUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rrRepo := &mockReactionRoleRepo{}
			vRepo := &mockVerificationRepo{}
			svc := service.NewReactionRoleService(rrRepo, vRepo, &mockAuditRepo{}, nil, staffRoles, verifiedRole, silentLogger)

			err := svc.CreateBinding(context.Background(), tc.actorID, tc.actorRoles, tc.rr)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestReactionRoleService_ResolveReactionForAdd(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	staffRoles := []string{"role_staff_1"}
	verifiedRole := "role_18_plus_verified"

	kinkRoleMapping := &domain.ReactionRole{
		GuildID:          "guild_1",
		MessageID:        "msg_100",
		EmojiAPIName:     "🪢",
		RoleID:           "role_shibari",
		RequiresVerified: true,
	}

	pronounRoleMapping := &domain.ReactionRole{
		GuildID:          "guild_1",
		MessageID:        "msg_200",
		EmojiAPIName:     "💜",
		RoleID:           "role_they_them",
		RequiresVerified: false,
	}

	tests := []struct {
		name       string
		mapping    *domain.ReactionRole
		verifiedDB *domain.Verification
		wantRoleID string
		wantErr    error
	}{
		{
			name:       "Success: 18+ Verified member claims kink role",
			mapping:    kinkRoleMapping,
			verifiedDB: &domain.Verification{DiscordUserID: "user_1", IsVerified: true},
			wantRoleID: "role_shibari",
			wantErr:    nil,
		},
		{
			name:       "Gate Block: Unverified member attempts to claim kink role",
			mapping:    kinkRoleMapping,
			verifiedDB: nil,
			wantRoleID: "",
			wantErr:    domain.ErrUnverifiedForRole,
		},
		{
			name:       "Success: Unverified member claims general pronoun role (RequiresVerified = false)",
			mapping:    pronounRoleMapping,
			verifiedDB: nil,
			wantRoleID: "role_they_them",
			wantErr:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rrRepo := &mockReactionRoleRepo{mapping: tc.mapping}
			vRepo := &mockVerificationRepo{existing: tc.verifiedDB}
			svc := service.NewReactionRoleService(rrRepo, vRepo, &mockAuditRepo{}, nil, staffRoles, verifiedRole, silentLogger)

			roleID, err := svc.ResolveReactionForAdd(context.Background(), "guild_1", "msg_100", "🪢", "user_1")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
			if roleID != tc.wantRoleID {
				t.Fatalf("expected role ID %q, got %q", tc.wantRoleID, roleID)
			}
		})
	}
}

func TestReactionRoleService_SavePanelMetadata(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	staffRoles := []string{"role_staff_1"}

	tests := []struct {
		name       string
		actorID    string
		actorRoles []string
		panel      *domain.ReactionPanel
		wantErr    error
	}{
		{
			name:       "Success: Staff saves valid panel",
			actorID:    "staff_1",
			actorRoles: []string{"role_staff_1"},
			panel: &domain.ReactionPanel{
				MessageID: "msg_1",
				GuildID:   "guild_1",
				ChannelID: "chan_1",
				Title:     "Roles",
			},
			wantErr: nil,
		},
		{
			name:       "RBAC Block: Regular user attempts to save panel",
			actorID:    "user_1",
			actorRoles: []string{"regular_role"},
			panel: &domain.ReactionPanel{
				MessageID: "msg_1",
				GuildID:   "guild_1",
				ChannelID: "chan_1",
				Title:     "Roles",
			},
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:       "Validation: Missing title",
			actorID:    "staff_1",
			actorRoles: []string{"role_staff_1"},
			panel: &domain.ReactionPanel{
				MessageID: "msg_1",
				GuildID:   "guild_1",
				ChannelID: "chan_1",
				Title:     "",
			},
			wantErr: domain.ErrInvalidInput,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rrRepo := &mockReactionRoleRepo{}
			vRepo := &mockVerificationRepo{}
			svc := service.NewReactionRoleService(rrRepo, vRepo, &mockAuditRepo{}, nil, staffRoles, "role_verified", silentLogger)

			err := svc.SavePanelMetadata(context.Background(), tc.actorID, tc.actorRoles, tc.panel)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestReactionRoleService_RemoveBinding(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	staffRoles := []string{"role_staff_1"}

	tests := []struct {
		name       string
		actorID    string
		actorRoles []string
		guildID    string
		messageID  string
		emoji      string
		wantErr    error
	}{
		{
			name:       "Success: Staff removes binding",
			actorID:    "staff_1",
			actorRoles: []string{"role_staff_1"},
			guildID:    "guild_1",
			messageID:  "msg_1",
			emoji:      "🪢",
			wantErr:    nil,
		},
		{
			name:       "RBAC Block: Member removes binding",
			actorID:    "user_1",
			actorRoles: []string{"user_role"},
			guildID:    "guild_1",
			messageID:  "msg_1",
			emoji:      "🪢",
			wantErr:    domain.ErrUnauthorized,
		},
		{
			name:       "Validation: Missing message ID",
			actorID:    "staff_1",
			actorRoles: []string{"role_staff_1"},
			guildID:    "guild_1",
			messageID:  "",
			emoji:      "🪢",
			wantErr:    domain.ErrInvalidInput,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rrRepo := &mockReactionRoleRepo{}
			vRepo := &mockVerificationRepo{}
			svc := service.NewReactionRoleService(rrRepo, vRepo, &mockAuditRepo{}, nil, staffRoles, "role_verified", silentLogger)

			err := svc.RemoveBinding(context.Background(), tc.actorID, tc.actorRoles, tc.guildID, tc.messageID, tc.emoji)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestReactionRoleService_ResolveReactionForRemove(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	mapping := &domain.ReactionRole{
		GuildID:      "guild_1",
		MessageID:    "msg_1",
		EmojiAPIName: "🪢",
		RoleID:       "role_kink",
	}

	rrRepo := &mockReactionRoleRepo{mapping: mapping}
	vRepo := &mockVerificationRepo{}
	svc := service.NewReactionRoleService(rrRepo, vRepo, &mockAuditRepo{}, nil, []string{"staff"}, "verified", silentLogger)

	// 1. Success
	roleID, err := svc.ResolveReactionForRemove(context.Background(), "guild_1", "msg_1", "🪢")
	if err != nil || roleID != "role_kink" {
		t.Fatalf("expected role_kink, got %q (err: %v)", roleID, err)
	}

	// 2. Not found
	rrRepo.mapping = nil
	_, err = svc.ResolveReactionForRemove(context.Background(), "guild_1", "msg_1", "🪢")
	if !errors.Is(err, domain.ErrReactionNotFound) {
		t.Fatalf("expected ErrReactionNotFound, got %v", err)
	}
}

func TestReactionRoleService_PanelAssetsAndImages(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}

	importOSWriteFile := func(name string) {
		p := tempDir + "/" + name
		_ = os.WriteFile(p, pngBytes, 0600)
	}
	importOSWriteFile("banner.png")
	importOSWriteFile("crest.png")

	assetStore, err := storage.NewLocalAssetStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	staffRoles := []string{"role_staff"}
	rrRepo := &mockReactionRoleRepo{
		panel: &domain.ReactionPanel{
			GuildID:   "guild_1",
			MessageID: "msg_1",
			Title:     "Old Panel",
		},
	}
	vRepo := &mockVerificationRepo{}
	svc := service.NewReactionRoleService(rrRepo, vRepo, &mockAuditRepo{}, assetStore, staffRoles, "role_verified", silentLogger)

	// 1. ListEC2ImageFiles
	files, err := svc.ListEC2ImageFiles()
	if err != nil || len(files) < 2 {
		t.Fatalf("expected at least 2 files, got %v (err: %v)", files, err)
	}

	// 2. LoadPanelAssets - Unauthorized
	_, _, err = svc.LoadPanelAssets(context.Background(), "user_1", []string{"member"}, "banner.png", "crest.png")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}

	// 3. LoadPanelAssets - Authorized Success
	banner, thumb, err := svc.LoadPanelAssets(context.Background(), "staff_1", staffRoles, "banner.png", "crest.png")
	if err != nil {
		t.Fatalf("unexpected error loading assets: %v", err)
	}
	if banner == nil || thumb == nil {
		t.Fatal("expected non-nil banner and thumb")
	}

	// 4. UpdatePanelImages - Clear Images
	panel, _, _, err := svc.UpdatePanelImages(context.Background(), "staff_1", staffRoles, "guild_1", "msg_1", "", "", true)
	if err != nil {
		t.Fatalf("unexpected error updating panel: %v", err)
	}
	if panel.BannerFile != "" || panel.ThumbnailFile != "" {
		t.Fatalf("expected cleared image filenames, got banner=%q thumb=%q", panel.BannerFile, panel.ThumbnailFile)
	}

	// 5. UpdatePanelImages - Set New Images
	panel, bAsset, tAsset, err := svc.UpdatePanelImages(context.Background(), "staff_1", staffRoles, "guild_1", "msg_1", "banner.png", "crest.png", false)
	if err != nil {
		t.Fatalf("unexpected error setting images: %v", err)
	}
	if panel.BannerFile != "banner.png" || panel.ThumbnailFile != "crest.png" || bAsset == nil || tAsset == nil {
		t.Fatalf("expected banner and thumb set, got panel=%+v", panel)
	}

	// 6. GetPanelMetadata
	meta, err := svc.GetPanelMetadata(context.Background(), "guild_1", "msg_1")
	if err != nil || meta == nil {
		t.Fatalf("expected panel metadata, got %v (err: %v)", meta, err)
	}

	// 7. ListMessageBindings
	bindings, err := svc.ListMessageBindings(context.Background(), "guild_1", "msg_1")
	if err != nil {
		t.Fatalf("unexpected error listing bindings: %v", err)
	}
	_ = bindings
}
