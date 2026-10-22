package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"syn/internal/domain"
	"syn/internal/service"
	"testing"
)

const (
	roleStaffID  = "role_staff_999"
	roleHelperID = "role_helper_888"
	roleMemberID = "role_regular_111"
)

type mockVerificationRepo struct {
	existing       *domain.Verification
	saveErr        error
	lastAuditEvent string
}

func (m *mockVerificationRepo) GetByUserID(_ context.Context, _, _ string) (*domain.Verification, error) {
	return m.existing, nil
}

func (m *mockVerificationRepo) SaveVerification(_ context.Context, _ *domain.Verification) error {
	return m.saveErr
}

func (m *mockVerificationRepo) LogAudit(_ context.Context, _, _, action string) error {
	m.lastAuditEvent = action
	return nil
}

func TestVerifyMember_RBAC(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	allowedRoles := []string{roleStaffID, roleHelperID}

	errDBDiskFull := errors.New("db disk full")

	tests := []struct {
		name           string
		guildID        string
		actorID        string
		actorRoles     []string
		targetUserID   string
		mockRepo       *mockVerificationRepo
		wantErr        error
		wantAuditEvent string
	}{
		{
			name:         "Success: Staff member verifies a user",
			guildID:      "guild_1",
			actorID:      "staff_user_1",
			actorRoles:   []string{roleMemberID, roleStaffID},
			targetUserID: "new_member_1",
			mockRepo:     &mockVerificationRepo{},
			wantErr:      nil,
		},
		{
			name:         "Success: Server Helper verifies a user",
			guildID:      "guild_1",
			actorID:      "helper_user_1",
			actorRoles:   []string{roleHelperID},
			targetUserID: "new_member_2",
			mockRepo:     &mockVerificationRepo{},
			wantErr:      nil,
		},
		{
			name:           "RBAC Block: Regular member attempts to run verify",
			guildID:        "guild_1",
			actorID:        "regular_user_1",
			actorRoles:     []string{roleMemberID},
			targetUserID:   "friend_user_2",
			mockRepo:       &mockVerificationRepo{},
			wantErr:        domain.ErrUnauthorized,
			wantAuditEvent: "UNAUTHORIZED_VERIFY_ATTEMPT",
		},
		{
			name:           "RBAC Block: User with no roles attempts to run verify",
			guildID:        "guild_1",
			actorID:        "unroled_user_1",
			actorRoles:     []string{},
			targetUserID:   "friend_user_2",
			mockRepo:       &mockVerificationRepo{},
			wantErr:        domain.ErrUnauthorized,
			wantAuditEvent: "UNAUTHORIZED_VERIFY_ATTEMPT",
		},
		{
			name:         "Success: Staff or Helper verifies themselves",
			guildID:      "guild_1",
			actorID:      "helper_user_1",
			actorRoles:   []string{roleHelperID},
			targetUserID: "helper_user_1",
			mockRepo:     &mockVerificationRepo{},
			wantErr:      nil,
		},
		{
			name:         "Idempotency: Member already verified",
			guildID:      "guild_1",
			actorID:      "staff_user_1",
			actorRoles:   []string{roleStaffID},
			targetUserID: "already_verified_user",
			mockRepo: &mockVerificationRepo{
				existing: &domain.Verification{
					DiscordUserID: "already_verified_user",
					GuildID:       "guild_1",
					IsVerified:    true,
				},
			},
			wantErr: domain.ErrAlreadyVerified,
		},
		{
			name:         "Validation: Missing guild ID",
			guildID:      "",
			actorID:      "staff_user_1",
			actorRoles:   []string{roleStaffID},
			targetUserID: "user_1",
			mockRepo:     &mockVerificationRepo{},
			wantErr:      domain.ErrInvalidInput,
		},
		{
			name:         "Validation: Missing target user ID",
			guildID:      "guild_1",
			actorID:      "staff_user_1",
			actorRoles:   []string{roleStaffID},
			targetUserID: "",
			mockRepo:     &mockVerificationRepo{},
			wantErr:      domain.ErrInvalidInput,
		},
		{
			name:         "Database Failure on Save",
			guildID:      "guild_1",
			actorID:      "staff_user_1",
			actorRoles:   []string{roleStaffID},
			targetUserID: "user_fail",
			mockRepo: &mockVerificationRepo{
				saveErr: errDBDiskFull,
			},
			wantErr: errDBDiskFull,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := service.NewVerificationService(tc.mockRepo, allowedRoles, silentLogger)

			_, err := svc.VerifyMember(context.Background(), tc.guildID, tc.actorID, tc.actorRoles, tc.targetUserID)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}

			if tc.wantAuditEvent != "" && tc.mockRepo.lastAuditEvent != tc.wantAuditEvent {
				t.Fatalf("expected audit event %q, got %q", tc.wantAuditEvent, tc.mockRepo.lastAuditEvent)
			}
		})
	}
}
