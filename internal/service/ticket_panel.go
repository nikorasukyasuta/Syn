package service

import (
	"context"

	"syn/internal/domain"
)

// SetPanelRepository configures the persistence backend for Ticket Hub posts.
func (s *TicketService) SetPanelRepository(panelRepo domain.TicketPanelRepository) {
	s.panelRepo = panelRepo
}

// GetOrInitPanelPost retrieves a stored TicketPanelPost or initializes the default definition.
func (s *TicketService) GetOrInitPanelPost(
	ctx context.Context,
	guildID string,
	panelType domain.TicketPanelType,
) (*domain.TicketPanelPost, error) {
	def := domain.DefaultTicketPanelPost(guildID, panelType)
	if def == nil {
		return nil, domain.ErrInvalidTicketType
	}

	if s.panelRepo != nil {
		stored, err := s.panelRepo.GetPanelPost(ctx, guildID, panelType)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			return stored, nil
		}
	}

	return def, nil
}

// ListPanelPosts returns all 5 Ticket Hub post configurations in their canonical display order,
// merging any saved customizations and message IDs from the database.
func (s *TicketService) ListPanelPosts(ctx context.Context, guildID string) ([]*domain.TicketPanelPost, error) {
	storedMap := make(map[domain.TicketPanelType]*domain.TicketPanelPost)
	if s.panelRepo != nil {
		storedList, err := s.panelRepo.ListPanelPosts(ctx, guildID)
		if err != nil {
			return nil, err
		}
		for _, p := range storedList {
			storedMap[p.PanelType] = p
		}
	}

	var result []*domain.TicketPanelPost
	for _, pt := range domain.OrderedTicketPanelTypes {
		if existing, ok := storedMap[pt]; ok {
			result = append(result, existing)
		} else {
			result = append(result, domain.DefaultTicketPanelPost(guildID, pt))
		}
	}

	return result, nil
}

// SavePanelPost persists a TicketPanelPost to the repository.
func (s *TicketService) SavePanelPost(ctx context.Context, post *domain.TicketPanelPost) error {
	if post == nil {
		return domain.ErrInvalidTicketType
	}
	if s.panelRepo == nil {
		return nil
	}
	return s.panelRepo.SavePanelPost(ctx, post)
}

