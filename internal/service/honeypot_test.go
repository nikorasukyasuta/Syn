package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/domain"
)

type fakeHoneypotRepo struct {
	mu          sync.Mutex
	cfg         *domain.HoneypotConfig
	quarantines map[string]*domain.SpamQuarantine
}

func newFakeHoneypotRepo() *fakeHoneypotRepo {
	return &fakeHoneypotRepo{
		quarantines: make(map[string]*domain.SpamQuarantine),
	}
}

func (r *fakeHoneypotRepo) SaveConfig(_ context.Context, cfg *domain.HoneypotConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *cfg
	r.cfg = &cp
	return nil
}

func (r *fakeHoneypotRepo) GetConfig(_ context.Context, _ string) (*domain.HoneypotConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg == nil {
		return nil, nil
	}
	cp := *r.cfg
	return &cp, nil
}

func (r *fakeHoneypotRepo) CreateQuarantine(_ context.Context, q *domain.SpamQuarantine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *q
	r.quarantines[q.UserID] = &cp
	return nil
}

func (r *fakeHoneypotRepo) GetActiveQuarantine(_ context.Context, _, userID string) (*domain.SpamQuarantine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q, ok := r.quarantines[userID]
	if !ok || q.Status != domain.QuarantineActive {
		return nil, domain.ErrNotQuarantined
	}
	cp := *q
	return &cp, nil
}

func (r *fakeHoneypotRepo) ListActiveQuarantines(_ context.Context, _ string) ([]*domain.SpamQuarantine, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var list []*domain.SpamQuarantine
	for _, q := range r.quarantines {
		if q.Status == domain.QuarantineActive {
			cp := *q
			list = append(list, &cp)
		}
	}
	return list, nil
}

func (r *fakeHoneypotRepo) ResolveQuarantine(_ context.Context, _, userID string, status domain.QuarantineStatus, resolvedBy string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if q, ok := r.quarantines[userID]; ok {
		q.Status = status
		q.ResolvedBy = resolvedBy
	}
	return nil
}

type fakeHoneypotDiscord struct {
	mu      sync.Mutex
	members map[string][]string // userID -> roleIDs
	roles   []*discordgo.Role
	kicked  map[string]string // userID -> reason
}

func newFakeHoneypotDiscord() *fakeHoneypotDiscord {
	return &fakeHoneypotDiscord{
		members: make(map[string][]string),
		kicked:  make(map[string]string),
		roles: []*discordgo.Role{
			{ID: "role-verified", Name: "18+ Verified"},
			{ID: "role-dm-open", Name: "DM's Open"},
			{ID: "role-spammer", Name: "Spammer"},
			{ID: "role-booster", Name: "Server Booster", Managed: true},
		},
	}
}

func (d *fakeHoneypotDiscord) GuildMember(_, userID string, _ ...discordgo.RequestOption) (*discordgo.Member, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	roles, ok := d.members[userID]
	if !ok {
		return nil, &discordgo.RESTError{
			Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMember, Message: "Unknown Member"},
		}
	}
	cp := append([]string(nil), roles...)
	return &discordgo.Member{
		User:  &discordgo.User{ID: userID},
		Roles: cp,
	}, nil
}

func (d *fakeHoneypotDiscord) GuildRoles(_ string, _ ...discordgo.RequestOption) ([]*discordgo.Role, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.roles, nil
}

func (d *fakeHoneypotDiscord) GuildMemberEdit(_, userID string, data *discordgo.GuildMemberParams, _ ...discordgo.RequestOption) (*discordgo.Member, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if data.Roles != nil {
		d.members[userID] = append([]string(nil), (*data.Roles)...)
	}
	return &discordgo.Member{
		User:  &discordgo.User{ID: userID},
		Roles: append([]string(nil), d.members[userID]...),
	}, nil
}

func (d *fakeHoneypotDiscord) GuildMemberRoleAdd(_, userID, roleID string, _ ...discordgo.RequestOption) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, r := range d.members[userID] {
		if r == roleID {
			return nil
		}
	}
	d.members[userID] = append(d.members[userID], roleID)
	return nil
}

func (d *fakeHoneypotDiscord) GuildMemberRoleRemove(_, userID, roleID string, _ ...discordgo.RequestOption) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var next []string
	for _, r := range d.members[userID] {
		if r != roleID {
			next = append(next, r)
		}
	}
	d.members[userID] = next
	return nil
}

func (d *fakeHoneypotDiscord) GuildMemberDeleteWithReason(_, userID, reason string, _ ...discordgo.RequestOption) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kicked[userID] = reason
	delete(d.members, userID)
	return nil
}

func TestHoneypotService_QuarantineAndRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo := newFakeHoneypotRepo()
	discord := newFakeHoneypotDiscord()
	discord.members["user-1"] = []string{"role-verified", "role-dm-open", "role-booster"}

	svc := NewHoneypotService(repo, nil, nil)
	if err := svc.Init(ctx, discord, "guild-1", "chan-honeypot", "role-spammer"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	q, created, err := svc.Quarantine(ctx, "guild-1", "user-1", "spambot99", "chan-honeypot", "msg-1")
	if err != nil {
		t.Fatalf("Quarantine failed: %v", err)
	}
	if !created || q == nil {
		t.Fatalf("expected quarantine to be created")
	}

	// Verify 3-day kick timer was set
	expectedKick := q.QuarantinedAt.Add(72 * time.Hour)
	if !q.KickAt.Equal(expectedKick) {
		t.Errorf("expected KickAt=%v, got %v", expectedKick, q.KickAt)
	}

	// Verify non-managed roles were stripped and Spammer + managed booster role remain
	discord.mu.Lock()
	currentRoles := append([]string(nil), discord.members["user-1"]...)
	discord.mu.Unlock()

	if !containsString(currentRoles, "role-spammer") {
		t.Errorf("expected member to have role-spammer, got %v", currentRoles)
	}
	if !containsString(currentRoles, "role-booster") {
		t.Errorf("expected managed booster role to be preserved, got %v", currentRoles)
	}
	if containsString(currentRoles, "role-verified") || containsString(currentRoles, "role-dm-open") {
		t.Errorf("expected regular roles to be stripped, got %v", currentRoles)
	}

	// Staff releases the member from quarantine
	releasedQ, err := svc.Release(ctx, "guild-1", "user-1", "staff-1")
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if releasedQ == nil {
		t.Fatalf("expected non-nil released quarantine")
	}

	// Verify original roles were restored and Spammer role was removed
	discord.mu.Lock()
	restoredRoles := append([]string(nil), discord.members["user-1"]...)
	discord.mu.Unlock()

	if containsString(restoredRoles, "role-spammer") {
		t.Errorf("expected role-spammer to be removed on release, got %v", restoredRoles)
	}
	if !containsString(restoredRoles, "role-verified") || !containsString(restoredRoles, "role-dm-open") {
		t.Errorf("expected stripped roles to be restored on release, got %v", restoredRoles)
	}
}

func TestHoneypotService_AutoKickAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo := newFakeHoneypotRepo()
	discord := newFakeHoneypotDiscord()
	discord.members["spammer-2"] = []string{"role-verified"}

	svc := NewHoneypotService(repo, nil, nil)
	svc.SetKickDelay(25 * time.Millisecond)

	outcomeCh := make(chan string, 1)
	svc.SetOutcomeHook(func(q *domain.SpamQuarantine, outcome string) {
		outcomeCh <- outcome
	})

	if err := svc.Init(ctx, discord, "guild-1", "chan-honeypot", "role-spammer"); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if _, created, err := svc.Quarantine(ctx, "guild-1", "spammer-2", "bot2", "chan-honeypot", "msg-2"); err != nil || !created {
		t.Fatalf("Quarantine failed: created=%v err=%v", created, err)
	}

	select {
	case outcome := <-outcomeCh:
		if outcome != OutcomeKicked {
			t.Fatalf("expected outcome %q, got %q", OutcomeKicked, outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for auto-kick to fire")
	}

	discord.mu.Lock()
	reason, wasKicked := discord.kicked["spammer-2"]
	discord.mu.Unlock()

	if !wasKicked || reason == "" {
		t.Errorf("expected spammer-2 to be kicked with reason, got wasKicked=%v reason=%q", wasKicked, reason)
	}
	if svc.GetActive("spammer-2") != nil {
		t.Errorf("expected spammer-2 to be removed from active quarantines after auto-kick")
	}
}

