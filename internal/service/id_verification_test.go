package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"syn/internal/domain"
	"syn/internal/service"
)

type mockIDVerificationRepo struct {
	mu       sync.Mutex
	requests map[string]*domain.IDVerificationRequest // by ID
}

func newMockIDVerificationRepo() *mockIDVerificationRepo {
	return &mockIDVerificationRepo{
		requests: make(map[string]*domain.IDVerificationRequest),
	}
}

func (m *mockIDVerificationRepo) CreateRequest(_ context.Context, req *domain.IDVerificationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if r.ChannelID == req.ChannelID {
			return domain.ErrIDVerifyRequestAlreadyOpen
		}
	}
	req.ID = "req-" + req.ChannelID
	m.requests[req.ID] = req
	return nil
}

func (m *mockIDVerificationRepo) GetRequestByChannelID(_ context.Context, guildID, channelID string) (*domain.IDVerificationRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if r.GuildID == guildID && r.ChannelID == channelID {
			return r, nil
		}
	}
	return nil, domain.ErrIDVerifyRequestNotFound
}

func (m *mockIDVerificationRepo) GetPendingRequestByUser(_ context.Context, guildID, userID string) (*domain.IDVerificationRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if r.GuildID == guildID && r.UserID == userID && r.Status == domain.IDVerificationStatusPending {
			return r, nil
		}
	}
	return nil, nil
}

func (m *mockIDVerificationRepo) UpdateRequestStatus(
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
	if r.Status != domain.IDVerificationStatusPending {
		return domain.ErrIDVerifyAlreadyReviewed
	}
	r.Status = status
	r.ReviewedByID = reviewerID
	r.ReviewReason = reason
	r.ReviewedAt = &reviewedAt
	return nil
}

type statefulVerifyRepo struct {
	mu      sync.Mutex
	records map[string]*domain.Verification
}

func newStatefulVerifyRepo() *statefulVerifyRepo {
	return &statefulVerifyRepo{
		records: make(map[string]*domain.Verification),
	}
}

func (s *statefulVerifyRepo) GetByUserID(_ context.Context, guildID, userID string) (*domain.Verification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[guildID+":"+userID]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (s *statefulVerifyRepo) SaveVerification(_ context.Context, v *domain.Verification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[v.GuildID+":"+v.DiscordUserID] = v
	return nil
}

func TestIDVerificationService_FlowAndRBAC(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	idRepo := newMockIDVerificationRepo()
	vRepo := newStatefulVerifyRepo()
	auditRepo := &mockAuditRepo{}

	svc := service.NewIDVerificationService(idRepo, vRepo, auditRepo, []string{roleStaffID, roleHelperID}, silentLogger)
	ctx := context.Background()

	// 1. User can open a new request when unverified
	existingReq, err := svc.CheckCanOpenRequest(ctx, "guild_1", "user_100")
	if err != nil || existingReq != nil {
		t.Fatalf("expected user_100 to be able to open request, got req=%v err=%v", existingReq, err)
	}

	req, err := svc.CreateRequest(ctx, "guild_1", "ch_id_100", "user_100")
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	if req.Status != domain.IDVerificationStatusPending {
		t.Fatalf("expected status pending, got %s", req.Status)
	}

	// 2. User cannot open a second pending request
	dupReq, err := svc.CheckCanOpenRequest(ctx, "guild_1", "user_100")
	if !errors.Is(err, domain.ErrIDVerifyRequestAlreadyOpen) || dupReq == nil {
		t.Fatalf("expected ErrIDVerifyRequestAlreadyOpen, got req=%v err=%v", dupReq, err)
	}

	// 3. Regular non-staff user CANNOT approve, deny, revoke, or check status
	_, _, err = svc.Approve(ctx, "guild_1", "attacker_1", []string{roleMemberID}, "user_100", "ch_id_100", "")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for non-staff approve, got %v", err)
	}
	_, err = svc.Deny(ctx, "guild_1", "attacker_1", []string{roleMemberID}, "user_100", "ch_id_100", "no")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for non-staff deny, got %v", err)
	}
	_, err = svc.Revoke(ctx, "guild_1", "attacker_1", []string{roleMemberID}, "user_100", "revoke")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for non-staff revoke, got %v", err)
	}
	_, _, err = svc.GetStatus(ctx, "guild_1", "attacker_1", []string{roleMemberID}, "user_100")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for non-staff status, got %v", err)
	}

	// 4. Staff approves user_100 inside ch_id_100
	ver, approvedReq, err := svc.Approve(ctx, "guild_1", "staff_1", []string{roleStaffID}, "", "ch_id_100", "Valid passport")
	if err != nil {
		t.Fatalf("unexpected error on staff approve: %v", err)
	}
	if !ver.IsVerified || ver.DiscordUserID != "user_100" {
		t.Fatalf("expected user_100 to be verified, got %+v", ver)
	}
	if approvedReq == nil || approvedReq.Status != domain.IDVerificationStatusApproved {
		t.Fatalf("expected request status approved, got %+v", approvedReq)
	}

	// 5. Verified user cannot open a new ID verification request
	_, err = svc.CheckCanOpenRequest(ctx, "guild_1", "user_100")
	if !errors.Is(err, domain.ErrAlreadyVerified) {
		t.Fatalf("expected ErrAlreadyVerified, got %v", err)
	}

	// 6. Staff checks status of user_100
	statusVer, statusPending, err := svc.GetStatus(ctx, "guild_1", "staff_1", []string{roleStaffID}, "user_100")
	if err != nil {
		t.Fatalf("unexpected error on GetStatus: %v", err)
	}
	if statusVer == nil || !statusVer.IsVerified {
		t.Fatalf("expected verified status for user_100")
	}
	if statusPending != nil {
		t.Fatalf("expected no pending request after approval, got %+v", statusPending)
	}

	// 7. Staff revokes verification of user_100
	revokedVer, err := svc.Revoke(ctx, "guild_1", "staff_1", []string{roleStaffID}, "user_100", "Expired ID")
	if err != nil {
		t.Fatalf("unexpected error on Revoke: %v", err)
	}
	if revokedVer.IsVerified {
		t.Fatalf("expected IsVerified=false after revoke")
	}

	// Revoking again when not verified returns ErrNotVerified
	_, err = svc.Revoke(ctx, "guild_1", "staff_1", []string{roleStaffID}, "user_100", "Again")
	if !errors.Is(err, domain.ErrNotVerified) {
		t.Fatalf("expected ErrNotVerified, got %v", err)
	}

	// 8. Test Deny flow for user_200
	_, err = svc.CreateRequest(ctx, "guild_1", "ch_id_200", "user_200")
	if err != nil {
		t.Fatalf("failed to create request for user_200: %v", err)
	}
	deniedReq, err := svc.Deny(ctx, "guild_1", "staff_1", []string{roleHelperID}, "user_200", "ch_id_200", "Blurry photo")
	if err != nil {
		t.Fatalf("unexpected error on Deny: %v", err)
	}
	if deniedReq.Status != domain.IDVerificationStatusDenied || deniedReq.ReviewReason != "Blurry photo" {
		t.Fatalf("unexpected denied request state: %+v", deniedReq)
	}
}

