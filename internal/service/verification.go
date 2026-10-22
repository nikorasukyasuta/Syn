package service

import (
	"context"
	"log/slog"
	"syn/internal/domain"
	"time"
)

// VerificationService orchestrates member age and community verification rules.
type VerificationService struct {
	repo           domain.VerificationRepository
	auditRepo      domain.AuditRepository
	allowedRoleSet map[string]struct{}
	logger         *slog.Logger
}

// NewVerificationService initializes the service with verification and audit repositories and allowed Staff role IDs.
func NewVerificationService(
	repo domain.VerificationRepository,
	auditRepo domain.AuditRepository,
	allowedRoleIDs []string,
	logger *slog.Logger,
) *VerificationService {
	roleSet := make(map[string]struct{}, len(allowedRoleIDs))
	for _, id := range allowedRoleIDs {
		roleSet[id] = struct{}{}
	}

	return &VerificationService{
		repo:           repo,
		auditRepo:      auditRepo,
		allowedRoleSet: roleSet,
		logger:         logger,
	}
}

// VerifyMember validates RBAC, business rules, and marks a Discord user as 18+ verified.
func (s *VerificationService) VerifyMember(
	ctx context.Context,
	guildID string,
	actorID string,
	actorRoleIDs []string,
	targetUserID string,
) (*domain.Verification, error) {
	if guildID == "" || actorID == "" || targetUserID == "" {
		return nil, domain.ErrInvalidInput
	}

	// 1. RBAC Policy Check: Ensure caller holds a Staff or Server Helper role.
	if !s.isAuthorized(actorRoleIDs) {
		s.logger.WarnContext(ctx, "unauthorized verification attempt",
			"actor_id", actorID,
			"target_id", targetUserID,
		)
		// Record the security violation in the database audit log.
		if s.auditRepo != nil {
			_ = s.auditRepo.LogAudit(ctx, actorID, targetUserID, "UNAUTHORIZED_VERIFY_ATTEMPT")
		}
		return nil, domain.ErrUnauthorized
	}

	// 2. Idempotency Check: Ensure target isn't already verified.
	existing, err := s.repo.GetByUserID(ctx, guildID, targetUserID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.IsVerified {
		return nil, domain.ErrAlreadyVerified
	}

	record := &domain.Verification{
		DiscordUserID: targetUserID,
		GuildID:       guildID,
		IsVerified:    true,
		VerifiedByID:  actorID,
		VerifiedAt:    time.Now().UTC(),
	}

	if err := s.repo.SaveVerification(ctx, record); err != nil {
		s.logger.ErrorContext(ctx, "failed to save verification", "error", err, "target_id", targetUserID)
		return nil, err
	}

	s.logger.InfoContext(ctx, "member verified by staff/helper", "actor_id", actorID, "target_id", targetUserID)
	return record, nil
}

// isAuthorized checks if the actor possesses at least one configured Staff or Helper role.
func (s *VerificationService) isAuthorized(actorRoles []string) bool {
	for _, r := range actorRoles {
		if _, ok := s.allowedRoleSet[r]; ok {
			return true
		}
	}
	return false
}
