package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"syn/internal/domain"
)

// TicketService coordinates business rules, RBAC checks, and persistence for support tickets.
type TicketService struct {
	repo         domain.TicketRepository
	staffRoleIDs []string
	logger       *slog.Logger
}

// NewTicketService constructs a new TicketService.
func NewTicketService(repo domain.TicketRepository, staffRoleIDs []string, logger *slog.Logger) *TicketService {
	return &TicketService{
		repo:         repo,
		staffRoleIDs: staffRoleIDs,
		logger:       logger,
	}
}

// CheckCanOpen validates that the ticket type is valid and that the user does not
// already possess an open ticket of the requested type.
// If an open ticket exists, returns that existing ticket along with domain.ErrTicketAlreadyOpen.
func (s *TicketService) CheckCanOpen(ctx context.Context, guildID, creatorID string, ticketType domain.TicketType) (*domain.Ticket, error) {
	if !ticketType.IsValid() {
		return nil, domain.ErrInvalidTicketType
	}

	// Filter violation tickets can always be opened even if an earlier violation ticket is active
	if ticketType == domain.TicketTypeFilteredWords {
		return nil, nil
	}

	existing, err := s.repo.GetActiveTicket(ctx, guildID, creatorID, ticketType)
	if err != nil {
		return nil, fmt.Errorf("checking for existing active ticket: %w", err)
	}
	if existing != nil {
		return existing, domain.ErrTicketAlreadyOpen
	}

	return nil, nil
}

// CreateTicket records a newly provisioned ticket channel in the database.
func (s *TicketService) CreateTicket(ctx context.Context, guildID, channelID, creatorID string, ticketType domain.TicketType) (*domain.Ticket, error) {
	if !ticketType.IsValid() {
		return nil, domain.ErrInvalidTicketType
	}
	if guildID == "" || channelID == "" || creatorID == "" {
		return nil, fmt.Errorf("guild_id, channel_id, and creator_id are required")
	}

	ticket := &domain.Ticket{
		GuildID:   guildID,
		ChannelID: channelID,
		CreatorID: creatorID,
		Type:      ticketType,
		Status:    domain.TicketStatusOpen,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.repo.CreateTicket(ctx, ticket); err != nil {
		return nil, err
	}

	s.logger.Info("ticket created and persisted",
		"ticket_id", ticket.ID,
		"guild_id", guildID,
		"channel_id", channelID,
		"creator_id", creatorID,
		"type", ticketType,
	)

	return ticket, nil
}

// CloseTicket marks an open ticket as closed, enforcing that only server staff
// or the original ticket creator has authorization to close it.
func (s *TicketService) CloseTicket(
	ctx context.Context,
	guildID, channelID, callerID string,
	callerRoles []string,
	resolution string,
) (*domain.Ticket, error) {
	ticket, err := s.repo.GetTicketByChannelID(ctx, guildID, channelID)
	if err != nil {
		return nil, err
	}

	if ticket.Status == domain.TicketStatusClosed {
		return ticket, domain.ErrTicketAlreadyClosed
	}

	if !s.CanCloseTicket(callerID, callerRoles, ticket) {
		s.logger.Warn("unauthorized attempt to close ticket",
			"ticket_id", ticket.ID,
			"caller_id", callerID,
			"creator_id", ticket.CreatorID,
		)
		return nil, domain.ErrUnauthorizedToClose
	}

	trimmedResolution := strings.TrimSpace(resolution)
	if trimmedResolution == "" {
		trimmedResolution = "Closed with no specific resolution notes"
	}

	now := time.Now().UTC()
	if err := s.repo.CloseTicket(ctx, guildID, channelID, callerID, trimmedResolution, now); err != nil {
		return nil, fmt.Errorf("persisting ticket close: %w", err)
	}

	ticket.Status = domain.TicketStatusClosed
	ticket.ClosedByID = callerID
	ticket.Resolution = trimmedResolution
	ticket.ClosedAt = &now

	s.logger.Info("ticket closed successfully",
		"ticket_id", ticket.ID,
		"guild_id", guildID,
		"channel_id", channelID,
		"closed_by_id", callerID,
		"resolution", trimmedResolution,
	)

	return ticket, nil
}

// GetTicketByChannel retrieves the ticket associated with a Discord channel.
func (s *TicketService) GetTicketByChannel(ctx context.Context, guildID, channelID string) (*domain.Ticket, error) {
	return s.repo.GetTicketByChannelID(ctx, guildID, channelID)
}

// CanCloseTicket returns true if the caller is the ticket creator or holds any staff role.
func (s *TicketService) CanCloseTicket(callerID string, callerRoles []string, ticket *domain.Ticket) bool {
	if ticket == nil {
		return false
	}
	// Ticket creator can always close their own ticket
	if callerID == ticket.CreatorID {
		return true
	}
	// Staff or Server Helpers can close tickets
	return s.IsStaff(callerRoles)
}

// IsStaff checks whether the given set of roles contains any authorized staff role.
func (s *TicketService) IsStaff(roles []string) bool {
	staffMap := make(map[string]struct{}, len(s.staffRoleIDs))
	for _, id := range s.staffRoleIDs {
		staffMap[id] = struct{}{}
	}
	for _, r := range roles {
		if _, exists := staffMap[r]; exists {
			return true
		}
	}
	return false
}

