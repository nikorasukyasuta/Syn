package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/domain"
	"syn/internal/service"
)

func TestTicketCommandDefinition(t *testing.T) {
	t.Parallel()

	cmd := TicketCommandDefinition()
	if cmd.Name != "ticket" {
		t.Fatalf("expected command name 'ticket', got %s", cmd.Name)
	}

	subCommands := make(map[string]*discordgo.ApplicationCommandOption)
	for _, opt := range cmd.Options {
		subCommands[opt.Name] = opt
	}

	if _, exists := subCommands["open"]; !exists {
		t.Fatal("expected 'open' subcommand")
	}
	if _, exists := subCommands["close"]; !exists {
		t.Fatal("expected 'close' subcommand")
	}
	if _, exists := subCommands["panel"]; !exists {
		t.Fatal("expected 'panel' subcommand")
	}
	if _, exists := subCommands["update-post"]; !exists {
		t.Fatal("expected 'update-post' subcommand")
	}

	openSub := subCommands["open"]
	typeOptFound := false
	for _, opt := range openSub.Options {
		if opt.Name == "type" {
			typeOptFound = true
			if len(opt.Choices) != 4 {
				t.Fatalf("expected 4 ticket type choices, got %d", len(opt.Choices))
			}
		}
	}
	if !typeOptFound {
		t.Fatal("expected 'type' option in /ticket open")
	}

	updatePostSub := subCommands["update-post"]
	updateTypeOptFound := false
	for _, opt := range updatePostSub.Options {
		if opt.Name == "type" {
			updateTypeOptFound = true
			if len(opt.Choices) != 5 {
				t.Fatalf("expected 5 panel post type choices (including id_verification), got %d", len(opt.Choices))
			}
		}
	}
	if !updateTypeOptFound {
		t.Fatal("expected 'type' option in /ticket update-post")
	}
}

func TestBuildTicketPanel(t *testing.T) {
	t.Parallel()

	embed := BuildTicketPanelEmbed()
	if embed == nil || embed.Title == "" {
		t.Fatal("expected non-empty panel embed")
	}

	buttons := BuildTicketPanelButtons()
	if len(buttons) != 2 {
		t.Fatalf("expected 2 action rows, got %d", len(buttons))
	}

	totalButtons := 0
	for _, comp := range buttons {
		if row, ok := comp.(discordgo.ActionsRow); ok {
			totalButtons += len(row.Components)
		}
	}
	if totalButtons != 4 {
		t.Fatalf("expected 4 buttons for the 4 general ticket types, got %d", totalButtons)
	}
}

func TestExtractModalInputs(t *testing.T) {
	t.Parallel()

	components := []discordgo.MessageComponent{
		&discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				&discordgo.TextInput{
					CustomID: "resolution_status",
					Value:    "Resolved",
				},
			},
		},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.TextInput{
					CustomID: "resolution_notes",
					Value:    "All issues solved cleanly",
				},
			},
		},
	}

	inputs := extractModalInputs(components)
	if inputs["resolution_status"] != "Resolved" {
		t.Fatalf("expected Resolved, got %s", inputs["resolution_status"])
	}
	if inputs["resolution_notes"] != "All issues solved cleanly" {
		t.Fatalf("expected 'All issues solved cleanly', got %s", inputs["resolution_notes"])
	}
}

func TestTicketHandler_RBAC_Panel(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		StaffRoleIDs: []string{"role_staff_1"},
	}
	svc := service.NewTicketService(nil, cfg.StaffRoleIDs, silentLogger)
	handler := NewTicketHandler(svc, cfg, nil, silentLogger)

	// User without staff role
	regularMember := &discordgo.Member{
		User:  &discordgo.User{ID: "user_regular"},
		Roles: []string{"role_regular"},
	}
	if svc.IsStaff(regularMember.Roles) {
		t.Fatal("expected regular member to NOT be staff")
	}

	// Staff member
	staffMember := &discordgo.Member{
		User:  &discordgo.User{ID: "user_staff"},
		Roles: []string{"role_staff_1"},
	}
	if !svc.IsStaff(staffMember.Roles) {
		t.Fatal("expected staff member to be staff")
	}

	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestTicketHandler_BuildChannelOverwrites(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		StaffRoleIDs: []string{"role_staff_1", "role_helper_2"},
	}
	svc := service.NewTicketService(nil, cfg.StaffRoleIDs, silentLogger)
	handler := NewTicketHandler(svc, cfg, nil, silentLogger)

	guildID := "guild_100"
	creatorID := "creator_200"
	botID := "bot_300"

	overwrites := handler.buildChannelOverwrites(guildID, creatorID, botID)

	// Overwrites should include:
	// 1: @everyone
	// 2: creator
	// 3: staff role 1
	// 4: helper role 2
	// 5: bot user
	if len(overwrites) != 5 {
		t.Fatalf("expected 5 overwrites, got %d", len(overwrites))
	}

	// Verify @everyone is denied ViewChannel
	if overwrites[0].ID != guildID || overwrites[0].Deny&discordgo.PermissionViewChannel == 0 {
		t.Fatal("expected @everyone to be denied ViewChannel")
	}

	// Verify creator has ViewChannel allowed
	if overwrites[1].ID != creatorID || overwrites[1].Allow&discordgo.PermissionViewChannel == 0 {
		t.Fatal("expected creator to be allowed ViewChannel")
	}

	// Verify staff has ViewChannel and ManageMessages allowed
	if overwrites[2].ID != "role_staff_1" || overwrites[2].Allow&discordgo.PermissionManageMessages == 0 {
		t.Fatal("expected staff to have ManageMessages allowed")
	}
}

func TestTicketHandler_BuildTicketHubOverwrites(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		GuildID:          "guild_100",
		UnverifiedRoleID: "role_unverified_1",
		VerifiedRoleID:   "role_verified_1",
		StaffRoleIDs:     []string{"role_staff_1"},
	}
	svc := service.NewTicketService(nil, cfg.StaffRoleIDs, silentLogger)
	handler := NewTicketHandler(svc, cfg, nil, silentLogger)

	overwrites := handler.buildTicketHubOverwrites("guild_100", "bot_999")

	// Verify @everyone (guild_100) can ViewChannel & ReadMessageHistory, but is denied SendMessages & AddReactions
	var everyoneOW *discordgo.PermissionOverwrite
	for _, ow := range overwrites {
		if ow.ID == "guild_100" {
			everyoneOW = ow
			break
		}
	}
	if everyoneOW == nil {
		t.Fatal("expected @everyone overwrite in Ticket Hub channel")
	}
	if everyoneOW.Allow&discordgo.PermissionViewChannel == 0 || everyoneOW.Allow&discordgo.PermissionReadMessageHistory == 0 {
		t.Fatal("expected @everyone to have ViewChannel and ReadMessageHistory allowed")
	}
	if everyoneOW.Deny&discordgo.PermissionSendMessages == 0 || everyoneOW.Deny&discordgo.PermissionAddReactions == 0 {
		t.Fatal("expected @everyone to have SendMessages and AddReactions denied (read-only)")
	}
}

func TestBuildSingleTicketPanelPost(t *testing.T) {
	t.Parallel()

	for _, pt := range domain.OrderedTicketPanelTypes {
		post := domain.DefaultTicketPanelPost("guild_100", pt)
		embed := BuildSingleTicketPanelEmbed(post)
		if embed == nil || embed.Title == "" || embed.Description == "" {
			t.Fatalf("expected valid embed for panel type %s", pt)
		}
		comps := BuildSingleTicketPanelButton(post)
		if len(comps) != 1 {
			t.Fatalf("expected 1 action row for panel type %s, got %d", pt, len(comps))
		}
		row, ok := comps[0].(discordgo.ActionsRow)
		if !ok || len(row.Components) != 1 {
			t.Fatalf("expected 1 button inside action row for panel type %s", pt)
		}
		btn, ok := row.Components[0].(discordgo.Button)
		if !ok || btn.CustomID != pt.CustomID() {
			t.Fatalf("expected button CustomID %s, got %v", pt.CustomID(), btn.CustomID)
		}
	}
}

type mockTicketHubPanelRepo struct {
	mu    sync.Mutex
	posts map[domain.TicketPanelType]*domain.TicketPanelPost
}

func newMockTicketHubPanelRepo() *mockTicketHubPanelRepo {
	return &mockTicketHubPanelRepo{
		posts: make(map[domain.TicketPanelType]*domain.TicketPanelPost),
	}
}

func (m *mockTicketHubPanelRepo) CreateTicket(_ context.Context, _ *domain.Ticket) error {
	return nil
}
func (m *mockTicketHubPanelRepo) GetTicketByChannelID(_ context.Context, _, _ string) (*domain.Ticket, error) {
	return nil, domain.ErrTicketNotFound
}
func (m *mockTicketHubPanelRepo) GetActiveTicket(_ context.Context, _, _ string, _ domain.TicketType) (*domain.Ticket, error) {
	return nil, nil
}
func (m *mockTicketHubPanelRepo) CloseTicket(_ context.Context, _, _, _, _ string, _ time.Time) error {
	return nil
}
func (m *mockTicketHubPanelRepo) ListOpenTicketsByCreator(_ context.Context, _, _ string) ([]*domain.Ticket, error) {
	return nil, nil
}
func (m *mockTicketHubPanelRepo) GetPanelPost(_ context.Context, guildID string, panelType domain.TicketPanelType) (*domain.TicketPanelPost, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.posts[panelType]; ok && p.GuildID == guildID {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}
func (m *mockTicketHubPanelRepo) ListPanelPosts(_ context.Context, guildID string) ([]*domain.TicketPanelPost, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []*domain.TicketPanelPost
	for _, pt := range domain.OrderedTicketPanelTypes {
		if p, ok := m.posts[pt]; ok && p.GuildID == guildID {
			cp := *p
			res = append(res, &cp)
		}
	}
	return res, nil
}
func (m *mockTicketHubPanelRepo) SavePanelPost(_ context.Context, post *domain.TicketPanelPost) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *post
	m.posts[post.PanelType] = &cp
	return nil
}

type mockHubRoundTripper struct {
	mu             sync.Mutex
	createdChanID  string
	sentMessages   []string
	editedMessages []string
}

func (rt *mockHubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	path := req.URL.Path
	method := req.Method

	// GuildChannels lookup
	if method == http.MethodGet && strings.HasSuffix(path, "/channels") {
		return jsonHTTPResponse(`[]`), nil
	}
	// GuildChannelCreateComplex
	if method == http.MethodPost && strings.HasSuffix(path, "/channels") {
		rt.createdChanID = "ch_ticket_hub_1"
		return jsonHTTPResponse(`{"id": "ch_ticket_hub_1", "name": "ticket-creation", "type": 0}`), nil
	}
	// Channel lookup
	if method == http.MethodGet && strings.Contains(path, "/channels/ch_ticket_hub_1") && !strings.Contains(path, "/messages") {
		return jsonHTTPResponse(`{"id": "ch_ticket_hub_1", "name": "ticket-creation", "type": 0}`), nil
	}
	// ChannelMessageSendComplex
	if method == http.MethodPost && strings.HasSuffix(path, "/messages") {
		msgID := fmt.Sprintf("msg_post_%d", len(rt.sentMessages)+1)
		rt.sentMessages = append(rt.sentMessages, msgID)
		return jsonHTTPResponse(fmt.Sprintf(`{"id": "%s", "channel_id": "ch_ticket_hub_1"}`, msgID)), nil
	}
	// ChannelMessageEditComplex (PATCH)
	if method == http.MethodPatch && strings.Contains(path, "/messages/") {
		parts := strings.Split(path, "/messages/")
		msgID := parts[len(parts)-1]
		rt.editedMessages = append(rt.editedMessages, msgID)
		return jsonHTTPResponse(fmt.Sprintf(`{"id": "%s", "channel_id": "ch_ticket_hub_1"}`, msgID)), nil
	}

	return jsonHTTPResponse(`{}`), nil
}

func jsonHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func TestTicketHandler_EnsureTicketHubAndInPlaceUpdate(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		GuildID:          "guild_100",
		TicketCategoryID: "cat_tickets_1",
		StaffRoleIDs:     []string{"role_staff_1"},
	}
	repo := newMockTicketHubPanelRepo()
	svc := service.NewTicketService(repo, cfg.StaffRoleIDs, silentLogger)
	handler := NewTicketHandler(svc, cfg, nil, silentLogger)

	rt := &mockHubRoundTripper{}
	session, _ := discordgo.New("Bot test_token")
	session.Client = &http.Client{Transport: rt}
	session.State.User = &discordgo.User{ID: "bot_1"}

	ctx := context.Background()

	// 1. Initial EnsureTicketHub creates the channel and sends 5 standalone posts (including ID verification)
	hubChID, err := handler.EnsureTicketHub(ctx, session, "guild_100")
	if err != nil {
		t.Fatalf("unexpected error in EnsureTicketHub: %v", err)
	}
	if hubChID != "ch_ticket_hub_1" {
		t.Fatalf("expected hub channel ID 'ch_ticket_hub_1', got %s", hubChID)
	}
	if len(rt.sentMessages) != 5 {
		t.Fatalf("expected 5 standalone messages sent on initial hub creation, got %d", len(rt.sentMessages))
	}
	if len(rt.editedMessages) != 0 {
		t.Fatalf("expected 0 edited messages on initial creation, got %d", len(rt.editedMessages))
	}

	// 2. Updating a single post (e.g. ID Verification or Bug Report) edits ONLY that message in-place without sending a new message
	idPost, err := svc.GetOrInitPanelPost(ctx, "guild_100", domain.PanelTypeIDVerification)
	if err != nil {
		t.Fatalf("failed to load id_verification post: %v", err)
	}
	idPost.Title = "🪪 Updated 18+ ID Verification Post"
	idPost.Description = "Updated instructions without moving post position!"

	if err := handler.syncSinglePanelPostInPlace(ctx, session, "guild_100", idPost); err != nil {
		t.Fatalf("unexpected error in syncSinglePanelPostInPlace: %v", err)
	}

	if len(rt.sentMessages) != 5 {
		t.Fatalf("expected no new messages to be sent on in-place update, got %d", len(rt.sentMessages))
	}
	if len(rt.editedMessages) != 1 || rt.editedMessages[0] != "msg_post_1" {
		t.Fatalf("expected msg_post_1 to be edited in-place, got %v", rt.editedMessages)
	}
}

