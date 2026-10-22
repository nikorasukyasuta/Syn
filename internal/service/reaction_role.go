package service

import (
	"context"
	"log/slog"

	"syn/internal/domain"
	"syn/internal/storage"
)

// ReactionRoleService manages reaction role bindings, panels, and local EC2 image assets.
type ReactionRoleService struct {
	rrRepo           domain.ReactionRoleRepository
	verifyRepo       domain.VerificationRepository
	auditRepo        domain.AuditRepository
	assets           *storage.LocalAssetStore
	staffRoleSet     map[string]struct{}
	protectedRoleSet map[string]struct{}
	logger           *slog.Logger
}

// NewReactionRoleService initializes the service with RBAC, protected role denylists, audit logging, and local EC2 storage.
func NewReactionRoleService(
	rrRepo domain.ReactionRoleRepository,
	verifyRepo domain.VerificationRepository,
	auditRepo domain.AuditRepository,
	assets *storage.LocalAssetStore,
	staffRoleIDs []string,
	verifiedRoleID string,
	logger *slog.Logger,
) *ReactionRoleService {
	staffSet := make(map[string]struct{}, len(staffRoleIDs))
	protectedSet := make(map[string]struct{}, len(staffRoleIDs)+1)

	for _, id := range staffRoleIDs {
		staffSet[id] = struct{}{}
		protectedSet[id] = struct{}{}
	}
	if verifiedRoleID != "" {
		protectedSet[verifiedRoleID] = struct{}{}
	}

	return &ReactionRoleService{
		rrRepo:           rrRepo,
		verifyRepo:       verifyRepo,
		auditRepo:        auditRepo,
		assets:           assets,
		staffRoleSet:     staffSet,
		protectedRoleSet: protectedSet,
		logger:           logger,
	}
}

// LoadPanelAssets validates RBAC and loads the requested banner/thumbnail files from the EC2 disk.
func (s *ReactionRoleService) LoadPanelAssets(
	ctx context.Context,
	actorID string,
	actorRoles []string,
	bannerFilename, thumbFilename string,
) (banner *storage.AssetFile, thumb *storage.AssetFile, err error) {
	if !s.isStaffOrHelper(actorRoles) {
		s.logAudit(ctx, actorID, bannerFilename, "UNAUTHORIZED_ASSET_READ_ATTEMPT")
		return nil, nil, domain.ErrUnauthorized
	}

	if bannerFilename != "" {
		banner, err = s.assets.LoadImage(bannerFilename)
		if err != nil {
			return nil, nil, err
		}
	}

	if thumbFilename != "" {
		thumb, err = s.assets.LoadImage(thumbFilename)
		if err != nil {
			return nil, nil, err
		}
	}

	return banner, thumb, nil
}

// SavePanelMetadata persists the panel configuration to PostgreSQL.
func (s *ReactionRoleService) SavePanelMetadata(
	ctx context.Context,
	actorID string,
	actorRoles []string,
	panel *domain.ReactionPanel,
) error {
	if panel == nil || panel.GuildID == "" || panel.ChannelID == "" || panel.MessageID == "" || panel.Title == "" {
		return domain.ErrInvalidInput
	}
	if !s.isStaffOrHelper(actorRoles) {
		s.logAudit(ctx, actorID, panel.MessageID, "UNAUTHORIZED_PANEL_SAVE_ATTEMPT")
		return domain.ErrUnauthorized
	}

	panel.CreatedByID = actorID
	return s.rrRepo.SavePanel(ctx, panel)
}

// UpdatePanelImages updates the local EC2 filenames associated with an existing panel and returns the loaded files.
func (s *ReactionRoleService) UpdatePanelImages(
	ctx context.Context,
	actorID string,
	actorRoles []string,
	guildID, messageID, bannerFile, thumbFile string,
	clearImages bool,
) (*domain.ReactionPanel, *storage.AssetFile, *storage.AssetFile, error) {
	if !s.isStaffOrHelper(actorRoles) {
		s.logAudit(ctx, actorID, messageID, "UNAUTHORIZED_PANEL_IMAGE_EDIT")
		return nil, nil, nil, domain.ErrUnauthorized
	}

	panel, err := s.rrRepo.GetPanel(ctx, guildID, messageID)
	if err != nil {
		return nil, nil, nil, err
	}
	if panel == nil {
		return nil, nil, nil, domain.ErrPanelNotFound
	}

	var bannerAsset, thumbAsset *storage.AssetFile

	if clearImages {
		panel.BannerFile = ""
		panel.ThumbnailFile = ""
	} else {
		if bannerFile != "" {
			bannerAsset, err = s.assets.LoadImage(bannerFile)
			if err != nil {
				return nil, nil, nil, err
			}
			panel.BannerFile = bannerAsset.Name
		} else if panel.BannerFile != "" {
			bannerAsset, _ = s.assets.LoadImage(panel.BannerFile)
		}

		if thumbFile != "" {
			thumbAsset, err = s.assets.LoadImage(thumbFile)
			if err != nil {
				return nil, nil, nil, err
			}
			panel.ThumbnailFile = thumbAsset.Name
		} else if panel.ThumbnailFile != "" {
			thumbAsset, _ = s.assets.LoadImage(panel.ThumbnailFile)
		}
	}

	if err := s.rrRepo.SavePanel(ctx, panel); err != nil {
		return nil, nil, nil, err
	}

	s.logAudit(ctx, actorID, messageID, "UPDATE_PANEL_IMAGES")
	return panel, bannerAsset, thumbAsset, nil
}

// ListEC2ImageFiles returns filenames in /app/assets/panels for Discord Slash Command Autocomplete.
func (s *ReactionRoleService) ListEC2ImageFiles() ([]string, error) {
	return s.assets.ListAvailableImages()
}

// GetPanelMetadata retrieves the stored visual layout for a panel message.
func (s *ReactionRoleService) GetPanelMetadata(ctx context.Context, guildID, messageID string) (*domain.ReactionPanel, error) {
	return s.rrRepo.GetPanel(ctx, guildID, messageID)
}

// CreateBinding allows Staff and Server Helpers to bind an emoji on a message to a self-assignable role.
func (s *ReactionRoleService) CreateBinding(
	ctx context.Context,
	actorID string,
	actorRoles []string,
	rr *domain.ReactionRole,
) error {
	if rr == nil || rr.GuildID == "" || rr.ChannelID == "" || rr.MessageID == "" || rr.EmojiAPIName == "" || rr.RoleID == "" {
		return domain.ErrInvalidInput
	}

	// 1. Verify caller is Staff or Server Helper
	if !s.isStaffOrHelper(actorRoles) {
		s.logAudit(ctx, actorID, rr.RoleID, "UNAUTHORIZED_REACTION_BIND_ATTEMPT")
		return domain.ErrUnauthorized
	}

	// 2. Privilege Escalation Guard: Prevent binding Staff, Helper, or 18+ Verified roles
	if _, isProtected := s.protectedRoleSet[rr.RoleID]; isProtected {
		s.logger.WarnContext(ctx, "blocked attempt to bind protected role to reaction",
			"actor_id", actorID,
			"role_id", rr.RoleID,
		)
		s.logAudit(ctx, actorID, rr.RoleID, "BLOCKED_PROTECTED_ROLE_BIND")
		return domain.ErrProtectedRole
	}

	rr.CreatedByID = actorID
	if err := s.rrRepo.UpsertMapping(ctx, rr); err != nil {
		s.logger.ErrorContext(ctx, "failed to save reaction role mapping", "error", err)
		return err
	}

	s.logger.InfoContext(ctx, "reaction role binding created",
		"actor_id", actorID,
		"message_id", rr.MessageID,
		"emoji", rr.EmojiAPIName,
		"role_id", rr.RoleID,
	)
	return nil
}

// ResolveReactionForAdd determines which Role ID to grant when a member reacts to a message.
func (s *ReactionRoleService) ResolveReactionForAdd(
	ctx context.Context,
	guildID, messageID, emojiAPIName, userID string,
) (string, error) {
	mapping, err := s.rrRepo.GetMapping(ctx, guildID, messageID, emojiAPIName)
	if err != nil {
		return "", err
	}
	if mapping == nil {
		return "", domain.ErrReactionNotFound
	}

	// Secondary Defense-in-Depth check against protected roles
	if _, isProtected := s.protectedRoleSet[mapping.RoleID]; isProtected {
		return "", domain.ErrProtectedRole
	}

	// Enforce 18+ Verification Gate if the role requires it
	if mapping.RequiresVerified {
		v, err := s.verifyRepo.GetByUserID(ctx, guildID, userID)
		if err != nil {
			return "", err
		}
		if v == nil || !v.IsVerified {
			return "", domain.ErrUnverifiedForRole
		}
	}

	return mapping.RoleID, nil
}

// ResolveReactionForRemove determines which Role ID to revoke when a member removes their reaction.
func (s *ReactionRoleService) ResolveReactionForRemove(
	ctx context.Context,
	guildID, messageID, emojiAPIName string,
) (string, error) {
	mapping, err := s.rrRepo.GetMapping(ctx, guildID, messageID, emojiAPIName)
	if err != nil {
		return "", err
	}
	if mapping == nil {
		return "", domain.ErrReactionNotFound
	}
	return mapping.RoleID, nil
}

func (s *ReactionRoleService) isStaffOrHelper(roles []string) bool {
	for _, r := range roles {
		if _, ok := s.staffRoleSet[r]; ok {
			return true
		}
	}
	return false
}

// RemoveBinding deletes an emoji-to-role link if the caller is Staff or a Server Helper.
func (s *ReactionRoleService) RemoveBinding(
	ctx context.Context,
	actorID string,
	actorRoles []string,
	guildID, messageID, emojiAPIName string,
) error {
	if guildID == "" || messageID == "" || emojiAPIName == "" {
		return domain.ErrInvalidInput
	}
	if !s.isStaffOrHelper(actorRoles) {
		s.logAudit(ctx, actorID, messageID, "UNAUTHORIZED_REACTION_REMOVE_ATTEMPT")
		return domain.ErrUnauthorized
	}

	if err := s.rrRepo.DeleteMapping(ctx, guildID, messageID, emojiAPIName); err != nil {
		return err
	}
	s.logAudit(ctx, actorID, messageID, "REMOVE_REACTION_ROLE")
	return nil
}

// ListMessageBindings retrieves all configured emoji-role pairs for a specific panel message.
func (s *ReactionRoleService) ListMessageBindings(
	ctx context.Context,
	guildID, messageID string,
) ([]*domain.ReactionRole, error) {
	return s.rrRepo.ListByMessage(ctx, guildID, messageID)
}

func (s *ReactionRoleService) logAudit(ctx context.Context, actorID, targetID, action string) {
	if s.auditRepo != nil {
		_ = s.auditRepo.LogAudit(ctx, actorID, targetID, action)
	}
}
