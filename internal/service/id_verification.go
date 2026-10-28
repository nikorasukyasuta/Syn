package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"syn/internal/domain"
)

// IDVerificationService orchestrates the dedicated 18+ ID verification workflow,
// enforcing staff-only RBAC for approvals, denials, revocations, and status checks.
type IDVerificationService struct {
	idRepo         domain.IDVerificationRepository
	verifyRepo     domain.VerificationRepository
	auditRepo      domain.AuditRepository
	allowedRoleSet map[string]struct{}
	logger         *slog.Logger
}

// NewIDVerificationService constructs a new IDVerificationService.
func NewIDVerificationService(
	idRepo domain.IDVerificationRepository,
	verifyRepo domain.VerificationRepository,
	auditRepo domain.AuditRepository,
	allowedRoleIDs []string,
	logger *slog.Logger,
) *IDVerificationService {
	roleSet := make(map[string]struct{}, len(allowedRoleIDs))
	for _, id := range allowedRoleIDs {
		roleSet[id] = struct{}{}
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &IDVerificationService{
		idRepo:         idRepo,
		verifyRepo:     verifyRepo,
		auditRepo:      auditRepo,
		allowedRoleSet: roleSet,
		logger:         logger,
	}
}

// IsStaff checks if the caller possesses at least one configured Staff or Server Helper role.
func (s *IDVerificationService) IsStaff(callerRoles []string) bool {
	for _, r := range callerRoles {
		if _, ok := s.allowedRoleSet[r]; ok {
			return true
		}
	}
	return false
}

// CheckCanOpenRequest verifies that a member does not already have an open pending ID verification channel
// and is not already verified.
func (s *IDVerificationService) CheckCanOpenRequest(
	ctx context.Context,
	guildID, userID string,
) (*domain.IDVerificationRequest, error) {
	if guildID == "" || userID == "" {
		return nil, domain.ErrInvalidInput
	}

	if s.idRepo != nil {
		pending, err := s.idRepo.GetPendingRequestByUser(ctx, guildID, userID)
		if err != nil {
			return nil, err
		}
		if pending != nil && pending.Status == domain.IDVerificationStatusPending {
			return pending, domain.ErrIDVerifyRequestAlreadyOpen
		}
	}

	if s.verifyRepo != nil {
		existing, err := s.verifyRepo.GetByUserID(ctx, guildID, userID)
		if err != nil {
			return nil, err
		}
		if existing != nil && existing.IsVerified {
			return nil, domain.ErrAlreadyVerified
		}
	}

	return nil, nil
}

// CreateRequest records a newly provisioned private ID verification channel in the database.
func (s *IDVerificationService) CreateRequest(
	ctx context.Context,
	guildID, channelID, userID string,
) (*domain.IDVerificationRequest, error) {
	if guildID == "" || channelID == "" || userID == "" {
		return nil, domain.ErrInvalidInput
	}

	req := &domain.IDVerificationRequest{
		GuildID:   guildID,
		ChannelID: channelID,
		UserID:    userID,
		Status:    domain.IDVerificationStatusPending,
		CreatedAt: time.Now().UTC(),
	}

	if s.idRepo != nil {
		if err := s.idRepo.CreateRequest(ctx, req); err != nil {
			return nil, err
		}
	}

	s.logger.InfoContext(ctx, "created ID verification request",
		"request_id", req.ID,
		"guild_id", guildID,
		"channel_id", channelID,
		"user_id", userID,
	)

	return req, nil
}

// GetRequestByChannel retrieves the ID verification request associated with a Discord channel.
func (s *IDVerificationService) GetRequestByChannel(
	ctx context.Context,
	guildID, channelID string,
) (*domain.IDVerificationRequest, error) {
	if s.idRepo == nil {
		return nil, domain.ErrIDVerifyRequestNotFound
	}
	return s.idRepo.GetRequestByChannelID(ctx, guildID, channelID)
}

// Approve validates staff RBAC, approves a member's 18+ ID verification, marks any pending request
// as approved, and persists the verification and audit records.
func (s *IDVerificationService) Approve(
	ctx context.Context,
	guildID, actorID string,
	actorRoles []string,
	targetUserID, channelID, notes string,
) (*domain.Verification, *domain.IDVerificationRequest, error) {
	if !s.IsStaff(actorRoles) {
		s.logger.WarnContext(ctx, "unauthorized ID verification approve attempt",
			"actor_id", actorID,
			"target_id", targetUserID,
		)
		if s.auditRepo != nil {
			_ = s.auditRepo.LogAudit(ctx, actorID, targetUserID, "UNAUTHORIZED_ID_VERIFY_ATTEMPT")
		}
		return nil, nil, domain.ErrUnauthorized
	}

	var req *domain.IDVerificationRequest
	if s.idRepo != nil {
		if channelID != "" {
			if r, err := s.idRepo.GetRequestByChannelID(ctx, guildID, channelID); err == nil && r != nil {
				req = r
			}
		}
		if req == nil && targetUserID != "" {
			if r, err := s.idRepo.GetPendingRequestByUser(ctx, guildID, targetUserID); err == nil && r != nil {
				req = r
			}
		}
	}

	if targetUserID == "" && req != nil {
		targetUserID = req.UserID
	}

	if guildID == "" || actorID == "" || targetUserID == "" {
		return nil, nil, domain.ErrInvalidInput
	}

	now := time.Now().UTC()
	trimmedNotes := strings.TrimSpace(notes)
	if trimmedNotes == "" {
		trimmedNotes = "Approved via 18+ ID Verification"
	}

	if req != nil && req.Status == domain.IDVerificationStatusPending && s.idRepo != nil {
		if err := s.idRepo.UpdateRequestStatus(ctx, req.ID, domain.IDVerificationStatusApproved, actorID, trimmedNotes, now); err != nil && !errors.Is(err, domain.ErrIDVerifyAlreadyReviewed) {
			return nil, nil, err
		}
		req.Status = domain.IDVerificationStatusApproved
		req.ReviewedByID = actorID
		req.ReviewReason = trimmedNotes
		req.ReviewedAt = &now
	}

	record := &domain.Verification{
		DiscordUserID: targetUserID,
		GuildID:       guildID,
		IsVerified:    true,
		VerifiedByID:  actorID,
		VerifiedAt:    now,
	}

	if s.verifyRepo != nil {
		if err := s.verifyRepo.SaveVerification(ctx, record); err != nil {
			s.logger.ErrorContext(ctx, "failed to save ID verification approval", "error", err, "target_id", targetUserID)
			return nil, nil, err
		}
	}

	if s.auditRepo != nil {
		_ = s.auditRepo.LogAudit(ctx, actorID, targetUserID, "ID_VERIFY_APPROVED")
	}

	s.logger.InfoContext(ctx, "approved member 18+ ID verification",
		"actor_id", actorID,
		"target_id", targetUserID,
	)

	return record, req, nil
}

// Deny validates staff RBAC and marks a member's pending ID verification request as denied.
func (s *IDVerificationService) Deny(
	ctx context.Context,
	guildID, actorID string,
	actorRoles []string,
	targetUserID, channelID, reason string,
) (*domain.IDVerificationRequest, error) {
	if !s.IsStaff(actorRoles) {
		s.logger.WarnContext(ctx, "unauthorized ID verification deny attempt",
			"actor_id", actorID,
			"target_id", targetUserID,
		)
		if s.auditRepo != nil {
			_ = s.auditRepo.LogAudit(ctx, actorID, targetUserID, "UNAUTHORIZED_ID_VERIFY_ATTEMPT")
		}
		return nil, domain.ErrUnauthorized
	}

	if guildID == "" || actorID == "" {
		return nil, domain.ErrInvalidInput
	}

	var req *domain.IDVerificationRequest
	if s.idRepo != nil {
		if channelID != "" {
			if r, err := s.idRepo.GetRequestByChannelID(ctx, guildID, channelID); err == nil && r != nil {
				req = r
			}
		}
		if req == nil && targetUserID != "" {
			if r, err := s.idRepo.GetPendingRequestByUser(ctx, guildID, targetUserID); err == nil && r != nil {
				req = r
			}
		}
	}

	if req == nil {
		return nil, domain.ErrIDVerifyRequestNotFound
	}
	if req.Status != domain.IDVerificationStatusPending {
		return nil, domain.ErrIDVerifyAlreadyReviewed
	}

	trimmedReason := strings.TrimSpace(reason)
	if trimmedReason == "" {
		trimmedReason = "ID verification requirements were not met"
	}

	now := time.Now().UTC()
	if s.idRepo != nil {
		if err := s.idRepo.UpdateRequestStatus(ctx, req.ID, domain.IDVerificationStatusDenied, actorID, trimmedReason, now); err != nil {
			return nil, err
		}
	}

	req.Status = domain.IDVerificationStatusDenied
	req.ReviewedByID = actorID
	req.ReviewReason = trimmedReason
	req.ReviewedAt = &now

	if s.auditRepo != nil {
		_ = s.auditRepo.LogAudit(ctx, actorID, req.UserID, "ID_VERIFY_DENIED")
	}

	s.logger.InfoContext(ctx, "denied member 18+ ID verification",
		"actor_id", actorID,
		"target_id", req.UserID,
		"reason", trimmedReason,
	)

	return req, nil
}

// Revoke validates staff RBAC and revokes a member's 18+ verification status.
func (s *IDVerificationService) Revoke(
	ctx context.Context,
	guildID, actorID string,
	actorRoles []string,
	targetUserID, reason string,
) (*domain.Verification, error) {
	if !s.IsStaff(actorRoles) {
		s.logger.WarnContext(ctx, "unauthorized ID verification revoke attempt",
			"actor_id", actorID,
			"target_id", targetUserID,
		)
		if s.auditRepo != nil {
			_ = s.auditRepo.LogAudit(ctx, actorID, targetUserID, "UNAUTHORIZED_ID_VERIFY_ATTEMPT")
		}
		return nil, domain.ErrUnauthorized
	}

	if guildID == "" || actorID == "" || targetUserID == "" {
		return nil, domain.ErrInvalidInput
	}

	if s.verifyRepo != nil {
		existing, err := s.verifyRepo.GetByUserID(ctx, guildID, targetUserID)
		if err != nil {
			return nil, err
		}
		if existing == nil || !existing.IsVerified {
			return nil, domain.ErrNotVerified
		}
	}

	now := time.Now().UTC()
	record := &domain.Verification{
		DiscordUserID: targetUserID,
		GuildID:       guildID,
		IsVerified:    false,
		VerifiedByID:  actorID,
		VerifiedAt:    now,
	}

	if s.verifyRepo != nil {
		if err := s.verifyRepo.SaveVerification(ctx, record); err != nil {
			return nil, err
		}
	}

	if s.auditRepo != nil {
		_ = s.auditRepo.LogAudit(ctx, actorID, targetUserID, "ID_VERIFY_REVOKED")
	}

	s.logger.InfoContext(ctx, "revoked member 18+ verification",
		"actor_id", actorID,
		"target_id", targetUserID,
		"reason", strings.TrimSpace(reason),
	)

	return record, nil
}

// GetStatus validates staff RBAC and returns a member's 18+ verification record and any pending request.
func (s *IDVerificationService) GetStatus(
	ctx context.Context,
	guildID, actorID string,
	actorRoles []string,
	targetUserID string,
) (*domain.Verification, *domain.IDVerificationRequest, error) {
	if !s.IsStaff(actorRoles) {
		if s.auditRepo != nil {
			_ = s.auditRepo.LogAudit(ctx, actorID, targetUserID, "UNAUTHORIZED_ID_VERIFY_ATTEMPT")
		}
		return nil, nil, domain.ErrUnauthorized
	}

	if guildID == "" || targetUserID == "" {
		return nil, nil, domain.ErrInvalidInput
	}

	var v *domain.Verification
	if s.verifyRepo != nil {
		res, err := s.verifyRepo.GetByUserID(ctx, guildID, targetUserID)
		if err != nil {
			return nil, nil, err
		}
		v = res
	}

	var pending *domain.IDVerificationRequest
	if s.idRepo != nil {
		res, err := s.idRepo.GetPendingRequestByUser(ctx, guildID, targetUserID)
		if err != nil {
			return nil, nil, err
		}
		pending = res
	}

	return v, pending, nil
}

