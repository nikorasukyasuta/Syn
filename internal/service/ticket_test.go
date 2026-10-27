package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"syn/internal/domain"
)

// mockTicketRepo provides an in-memory mock implementation of domain.TicketRepository
// and domain.TicketPanelRepository for unit testing.
type mockTicketRepo struct {
	ticketsByChannel map[string]*domain.Ticket
	panelPosts       map[domain.TicketPanelType]*domain.TicketPanelPost
	createErr        error
	closeErr         error
}

func newMockTicketRepo() *mockTicketRepo {
	return &mockTicketRepo{
		ticketsByChannel: make(map[string]*domain.Ticket),
		panelPosts:       make(map[domain.TicketPanelType]*domain.TicketPanelPost),
	}
}

func (m *mockTicketRepo) GetPanelPost(ctx context.Context, guildID string, panelType domain.TicketPanelType) (*domain.TicketPanelPost, error) {
	if p, ok := m.panelPosts[panelType]; ok && p.GuildID == guildID {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}

func (m *mockTicketRepo) ListPanelPosts(ctx context.Context, guildID string) ([]*domain.TicketPanelPost, error) {
	var list []*domain.TicketPanelPost
	for _, pt := range domain.OrderedTicketPanelTypes {
		if p, ok := m.panelPosts[pt]; ok && p.GuildID == guildID {
			cp := *p
			list = append(list, &cp)
		}
	}
	return list, nil
}

func (m *mockTicketRepo) SavePanelPost(ctx context.Context, post *domain.TicketPanelPost) error {
	cp := *post
	cp.UpdatedAt = time.Now().UTC()
	m.panelPosts[post.PanelType] = &cp
	return nil
}

func (m *mockTicketRepo) CreateTicket(ctx context.Context, t *domain.Ticket) error {
	if m.createErr != nil {
		return m.createErr
	}
	// Check for active open ticket
	for _, existing := range m.ticketsByChannel {
		if existing.GuildID == t.GuildID &&
			existing.CreatorID == t.CreatorID &&
			existing.Type == t.Type &&
			existing.Status == domain.TicketStatusOpen {
			return domain.ErrTicketAlreadyOpen
		}
	}
	t.ID = "mock-uuid-" + t.ChannelID
	m.ticketsByChannel[t.ChannelID] = t
	return nil
}

func (m *mockTicketRepo) GetTicketByChannelID(ctx context.Context, guildID, channelID string) (*domain.Ticket, error) {
	t, exists := m.ticketsByChannel[channelID]
	if !exists || t.GuildID != guildID {
		return nil, domain.ErrTicketNotFound
	}
	return t, nil
}

func (m *mockTicketRepo) GetActiveTicket(ctx context.Context, guildID, creatorID string, ticketType domain.TicketType) (*domain.Ticket, error) {
	for _, t := range m.ticketsByChannel {
		if t.GuildID == guildID && t.CreatorID == creatorID && t.Type == ticketType && t.Status == domain.TicketStatusOpen {
			return t, nil
		}
	}
	return nil, nil
}

func (m *mockTicketRepo) CloseTicket(ctx context.Context, guildID, channelID, closedByID, resolution string, closedAt time.Time) error {
	if m.closeErr != nil {
		return m.closeErr
	}
	t, exists := m.ticketsByChannel[channelID]
	if !exists || t.GuildID != guildID {
		return domain.ErrTicketNotFound
	}
	if t.Status == domain.TicketStatusClosed {
		return domain.ErrTicketAlreadyClosed
	}
	t.Status = domain.TicketStatusClosed
	t.ClosedByID = closedByID
	t.Resolution = resolution
	t.ClosedAt = &closedAt
	return nil
}

func (m *mockTicketRepo) ListOpenTicketsByCreator(ctx context.Context, guildID, creatorID string) ([]*domain.Ticket, error) {
	var list []*domain.Ticket
	for _, t := range m.ticketsByChannel {
		if t.GuildID == guildID && t.CreatorID == creatorID && t.Status == domain.TicketStatusOpen {
			list = append(list, t)
		}
	}
	return list, nil
}

func TestTicketService_CheckCanOpen(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	repo := newMockTicketRepo()
	svc := NewTicketService(repo, []string{"role_staff_1"}, silentLogger)

	ctx := context.Background()
	guildID := "guild_1"
	userID := "user_1"

	// 1. Invalid ticket type
	_, err := svc.CheckCanOpen(ctx, guildID, userID, domain.TicketType("invalid_type"))
	if err != domain.ErrInvalidTicketType {
		t.Fatalf("expected ErrInvalidTicketType, got %v", err)
	}

	// 2. Can open when none exists
	existing, err := svc.CheckCanOpen(ctx, guildID, userID, domain.TicketTypeSupport)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if existing != nil {
		t.Fatalf("expected nil existing ticket, got %v", existing)
	}

	// 3. Create an open support ticket
	_, err = svc.CreateTicket(ctx, guildID, "ch_support_1", userID, domain.TicketTypeSupport)
	if err != nil {
		t.Fatalf("failed to create ticket: %v", err)
	}

	// 4. Cannot open another support ticket (same type)
	existing, err = svc.CheckCanOpen(ctx, guildID, userID, domain.TicketTypeSupport)
	if err != domain.ErrTicketAlreadyOpen {
		t.Fatalf("expected ErrTicketAlreadyOpen, got %v", err)
	}
	if existing == nil || existing.ChannelID != "ch_support_1" {
		t.Fatalf("expected existing ticket channel ch_support_1, got %v", existing)
	}

	// 5. CAN open a different type (e.g., Bug ticket)
	existing, err = svc.CheckCanOpen(ctx, guildID, userID, domain.TicketTypeBug)
	if err != nil {
		t.Fatalf("expected to allow Bug ticket even if Support ticket is open, got error: %v", err)
	}
	if existing != nil {
		t.Fatalf("expected nil existing for Bug, got %v", existing)
	}
}

func TestTicketService_CreateTicket(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	repo := newMockTicketRepo()
	svc := NewTicketService(repo, []string{"role_staff_1"}, silentLogger)

	ctx := context.Background()

	// 1. Invalid ticket type
	_, err := svc.CreateTicket(ctx, "guild_1", "ch_1", "user_1", domain.TicketType("unknown"))
	if err != domain.ErrInvalidTicketType {
		t.Fatalf("expected ErrInvalidTicketType, got %v", err)
	}

	// 2. Missing fields
	_, err = svc.CreateTicket(ctx, "", "ch_1", "user_1", domain.TicketTypeVerification)
	if err == nil {
		t.Fatal("expected error on empty guild_id, got nil")
	}

	// 3. Successful creation
	created, err := svc.CreateTicket(ctx, "guild_1", "ch_verify_1", "user_1", domain.TicketTypeVerification)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.Status != domain.TicketStatusOpen {
		t.Fatalf("expected status open, got %s", created.Status)
	}
	if created.Type != domain.TicketTypeVerification {
		t.Fatalf("expected type verification, got %s", created.Type)
	}

	// 4. Duplicate open ticket of same type fails
	_, err = svc.CreateTicket(ctx, "guild_1", "ch_verify_2", "user_1", domain.TicketTypeVerification)
	if err != domain.ErrTicketAlreadyOpen {
		t.Fatalf("expected ErrTicketAlreadyOpen, got %v", err)
	}
}

func TestTicketService_CloseTicket(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	repo := newMockTicketRepo()
	staffRole := "role_staff_100"
	svc := NewTicketService(repo, []string{staffRole}, silentLogger)

	ctx := context.Background()
	guildID := "guild_1"
	creatorID := "user_creator"
	chID := "ch_report_1"

	_, err := svc.CreateTicket(ctx, guildID, chID, creatorID, domain.TicketTypeReport)
	if err != nil {
		t.Fatalf("failed to create ticket: %v", err)
	}

	// 1. Unauthorized third-party user cannot close ticket
	unauthorizedUser := "user_intruder"
	_, err = svc.CloseTicket(ctx, guildID, chID, unauthorizedUser, []string{"role_regular"}, "closing unauthorized")
	if err != domain.ErrUnauthorizedToClose {
		t.Fatalf("expected ErrUnauthorizedToClose, got %v", err)
	}

	// 2. Creator CAN close their own ticket
	closedByCreator, err := svc.CloseTicket(ctx, guildID, chID, creatorID, []string{"role_regular"}, "Issue self-resolved")
	if err != nil {
		t.Fatalf("unexpected error closing by creator: %v", err)
	}
	if closedByCreator.Status != domain.TicketStatusClosed {
		t.Fatalf("expected status closed, got %s", closedByCreator.Status)
	}
	if closedByCreator.ClosedByID != creatorID {
		t.Fatalf("expected closed_by %s, got %s", creatorID, closedByCreator.ClosedByID)
	}
	if closedByCreator.Resolution != "Issue self-resolved" {
		t.Fatalf("expected resolution 'Issue self-resolved', got %s", closedByCreator.Resolution)
	}

	// 3. Trying to close an already-closed ticket returns ErrTicketAlreadyClosed
	_, err = svc.CloseTicket(ctx, guildID, chID, creatorID, []string{}, "close again")
	if err != domain.ErrTicketAlreadyClosed {
		t.Fatalf("expected ErrTicketAlreadyClosed, got %v", err)
	}

	// 4. Create another ticket and verify STAFF can close it
	chID2 := "ch_bug_2"
	_, err = svc.CreateTicket(ctx, guildID, chID2, creatorID, domain.TicketTypeBug)
	if err != nil {
		t.Fatalf("failed to create second ticket: %v", err)
	}

	modUser := "user_moderator"
	closedByMod, err := svc.CloseTicket(ctx, guildID, chID2, modUser, []string{staffRole}, "   ")
	if err != nil {
		t.Fatalf("unexpected error closing by staff: %v", err)
	}
	if closedByMod.ClosedByID != modUser {
		t.Fatalf("expected closed_by %s, got %s", modUser, closedByMod.ClosedByID)
	}
	// Blank resolution defaults to fallback note
	if closedByMod.Resolution != "Closed with no specific resolution notes" {
		t.Fatalf("expected fallback resolution, got %s", closedByMod.Resolution)
	}
}

func TestTicketType_Helpers(t *testing.T) {
	t.Parallel()

	for _, tt := range domain.AllTicketTypes {
		if !tt.IsValid() {
			t.Fatalf("expected valid for %s", tt)
		}
		if tt.DisplayName() == "" {
			t.Fatalf("expected non-empty display name for %s", tt)
		}
		if tt.Emoji() == "" {
			t.Fatalf("expected non-empty emoji for %s", tt)
		}
		if tt.ChannelPrefix() == "" {
			t.Fatalf("expected non-empty prefix for %s", tt)
		}
	}

	parsed, err := domain.ParseTicketType("verification")
	if err != nil || parsed != domain.TicketTypeVerification {
		t.Fatalf("expected verification, got %s (err: %v)", parsed, err)
	}
	parsed, err = domain.ParseTicketType("account_issue")
	if err != nil || parsed != domain.TicketTypeAccountIssue {
		t.Fatalf("expected account_issue, got %s (err: %v)", parsed, err)
	}
	_, err = domain.ParseTicketType("non_existent")
	if err != domain.ErrInvalidTicketType {
		t.Fatalf("expected ErrInvalidTicketType, got %v", err)
	}
}

func TestTicketService_PanelPosts(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	repo := newMockTicketRepo()
	svc := NewTicketService(repo, []string{"role_staff_1"}, silentLogger)

	ctx := context.Background()
	guildID := "guild_hub_1"

	// 1. ListPanelPosts initializes and returns all 5 ordered ticket flows (including ID verification)
	posts, err := svc.ListPanelPosts(ctx, guildID)
	if err != nil {
		t.Fatalf("unexpected error listing panel posts: %v", err)
	}
	if len(posts) != 5 {
		t.Fatalf("expected 5 panel posts (ID verification + 4 ticket types), got %d", len(posts))
	}
	if posts[0].PanelType != domain.PanelTypeIDVerification {
		t.Fatalf("expected first post to be id_verification, got %s", posts[0].PanelType)
	}
	if posts[0].PanelType.CustomID() != "btn_id_verify_start" {
		t.Fatalf("expected id_verification customID 'btn_id_verify_start', got %s", posts[0].PanelType.CustomID())
	}

	// 2. Save custom title/description and MessageID for a post and verify in-place persistence
	supportPost, err := svc.GetOrInitPanelPost(ctx, guildID, domain.PanelTypeSupport)
	if err != nil {
		t.Fatalf("unexpected error getting support post: %v", err)
	}
	supportPost.ChannelID = "ch_hub_999"
	supportPost.MessageID = "msg_support_123"
	supportPost.Title = "💬 Custom Support Flow"
	supportPost.Description = "Updated instructions for support tickets."
	supportPost.ButtonLabel = "Request Help"

	if err := svc.SavePanelPost(ctx, supportPost); err != nil {
		t.Fatalf("failed to save panel post: %v", err)
	}

	reloaded, err := svc.GetOrInitPanelPost(ctx, guildID, domain.PanelTypeSupport)
	if err != nil {
		t.Fatalf("failed to reload support post: %v", err)
	}
	if reloaded.MessageID != "msg_support_123" || reloaded.Title != "💬 Custom Support Flow" {
		t.Fatalf("expected updated support post fields, got %+v", reloaded)
	}
}
