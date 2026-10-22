package service_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"syn/internal/domain"
	"syn/internal/service"
)

type mockWarningRepo struct {
	warnings   []*domain.UserWarning
	violations map[string]*domain.FilterViolation // channelID -> violation
}

func newMockWarningRepo() *mockWarningRepo {
	return &mockWarningRepo{
		violations: make(map[string]*domain.FilterViolation),
	}
}

func (m *mockWarningRepo) SaveWarning(ctx context.Context, w *domain.UserWarning) error {
	w.ID = "warn_123"
	w.CreatedAt = time.Now().UTC()
	m.warnings = append(m.warnings, w)
	return nil
}

func (m *mockWarningRepo) GetWarningsByUserID(ctx context.Context, guildID, userID string) ([]*domain.UserWarning, error) {
	var res []*domain.UserWarning
	for _, w := range m.warnings {
		if w.GuildID == guildID && w.UserID == userID {
			res = append(res, w)
		}
	}
	return res, nil
}

func (m *mockWarningRepo) GetWarningCountByUserID(ctx context.Context, guildID, userID string) (int, error) {
	count := 0
	for _, w := range m.warnings {
		if w.GuildID == guildID && w.UserID == userID {
			count++
		}
	}
	return count, nil
}

func (m *mockWarningRepo) SaveViolation(ctx context.Context, v *domain.FilterViolation) error {
	v.ID = "viol_123"
	v.CreatedAt = time.Now().UTC()
	m.violations[v.ChannelID] = v
	return nil
}

func (m *mockWarningRepo) GetViolationByChannelID(ctx context.Context, guildID, channelID string) (*domain.FilterViolation, error) {
	if v, ok := m.violations[channelID]; ok && v.GuildID == guildID {
		return v, nil
	}
	return nil, nil
}

func TestWordFilterService_Matching(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc := service.NewWordFilterService(nil, []string{"role_staff"}, silentLogger)

	svc.SetGlobalWords([]string{"bannedword", "toxic phrase"})
	svc.SetChannelWords("chan_1", []string{"secretcode"})

	tests := []struct {
		name      string
		channelID string
		content   string
		wantMatch bool
		wantWord  string
		wantScope domain.FilterScope
	}{
		{
			name:      "Global exact match lower",
			channelID: "any_chan",
			content:   "this contains bannedword here",
			wantMatch: true,
			wantWord:  "bannedword",
			wantScope: domain.FilterScopeGlobal,
		},
		{
			name:      "Global case insensitive match",
			channelID: "any_chan",
			content:   "THIS CONTAINS BANNEDWORD!",
			wantMatch: true,
			wantWord:  "bannedword",
			wantScope: domain.FilterScopeGlobal,
		},
		{
			name:      "Global mixed casing",
			channelID: "any_chan",
			content:   "Hello BaNnEdWoRd world",
			wantMatch: true,
			wantWord:  "bannedword",
			wantScope: domain.FilterScopeGlobal,
		},
		{
			name:      "Global phrase matching",
			channelID: "any_chan",
			content:   "You are saying a TOXIC PHRASE right now",
			wantMatch: true,
			wantWord:  "toxic phrase",
			wantScope: domain.FilterScopeGlobal,
		},
		{
			name:      "Prevent false positives for substrings",
			channelID: "any_chan",
			content:   "notbannedword or bannedwordsmith",
			wantMatch: false,
		},
		{
			name:      "Channel filter matched in target channel",
			channelID: "chan_1",
			content:   "Here is the SECRETCODE for you",
			wantMatch: true,
			wantWord:  "secretcode",
			wantScope: domain.FilterScopeChannel,
		},
		{
			name:      "Channel filter not triggered in different channel",
			channelID: "chan_2",
			content:   "Here is the secretcode in another channel",
			wantMatch: false,
		},
		{
			name:      "Clean message passes",
			channelID: "chan_1",
			content:   "Just a completely regular helpful message",
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			matched, match := svc.CheckMessage(tc.channelID, tc.content)
			if matched != tc.wantMatch {
				t.Fatalf("expected matched=%v, got=%v for content: %q", tc.wantMatch, matched, tc.content)
			}
			if tc.wantMatch {
				if match.Word != tc.wantWord {
					t.Errorf("expected match word %q, got %q", tc.wantWord, match.Word)
				}
				if match.Scope != tc.wantScope {
					t.Errorf("expected match scope %q, got %q", tc.wantScope, match.Scope)
				}
			}
		})
	}
}

func TestWordFilterService_LoadAndReload(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc := service.NewWordFilterService(nil, []string{"role_staff"}, silentLogger)

	// 1. Create global.json (array format)
	globalFile := filepath.Join(tmpDir, "global.json")
	if err := os.WriteFile(globalFile, []byte(`["apple", "banana"]`), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Create channel file (object format)
	chanFile := filepath.Join(tmpDir, "12345.json")
	if err := os.WriteFile(chanFile, []byte(`{"words": ["cherry", "date"]}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Load all
	if err := svc.LoadAll(tmpDir); err != nil {
		t.Fatalf("failed to load all: %v", err)
	}

	if len(svc.GetGlobalWords()) != 2 {
		t.Fatalf("expected 2 global words, got %d", len(svc.GetGlobalWords()))
	}
	if len(svc.GetChannelWords("12345")) != 2 {
		t.Fatalf("expected 2 channel words for 12345, got %d", len(svc.GetChannelWords("12345")))
	}

	// 3. Update global.json and reload
	if err := os.WriteFile(globalFile, []byte(`["apple", "banana", "elderberry"]`), 0644); err != nil {
		t.Fatal(err)
	}
	found, count, err := svc.ReloadGlobal(tmpDir)
	if err != nil || !found || count != 3 {
		t.Fatalf("expected reload success with count 3, got found=%v count=%d err=%v", found, count, err)
	}
	if len(svc.GetGlobalWords()) != 3 {
		t.Fatalf("expected 3 global words after reload, got %d", len(svc.GetGlobalWords()))
	}

	// 4. Reload non-existent channel filter: should NOT wipe existing cache
	nonExistentChan := "99999"
	found, count, err = svc.ReloadChannel(tmpDir, nonExistentChan)
	if err != nil {
		t.Fatalf("expected nil error on missing file, got %v", err)
	}
	if found {
		t.Fatal("expected found=false for missing channel filter file")
	}
	if count != 0 {
		t.Fatalf("expected count=0, got %d", count)
	}

	// Channel 12345 cache must still be intact
	if len(svc.GetChannelWords("12345")) != 2 {
		t.Fatalf("expected channel 12345 cache to remain 2 words, got %d", len(svc.GetChannelWords("12345")))
	}
}

func TestWordFilterService_WarningsAndViolations(t *testing.T) {
	t.Parallel()

	repo := newMockWarningRepo()
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	svc := service.NewWordFilterService(repo, []string{"role_staff_admin"}, silentLogger)

	ctx := context.Background()

	// 1. Save violation
	violation := &domain.FilterViolation{
		GuildID:        "guild_1",
		ChannelID:      "ticket_chan_1",
		UserID:         "bad_user_1",
		MessageContent: "I wrote badword",
		MatchedWord:    "badword",
		OriginChannel:  "general_chat",
	}
	if err := svc.SaveViolation(ctx, violation); err != nil {
		t.Fatalf("failed to save violation: %v", err)
	}

	// Retrieve violation
	retrieved, err := svc.GetViolation(ctx, "guild_1", "ticket_chan_1")
	if err != nil || retrieved == nil {
		t.Fatalf("expected non-nil violation, got %v (err: %v)", retrieved, err)
	}
	if retrieved.UserID != "bad_user_1" {
		t.Errorf("expected user ID bad_user_1, got %s", retrieved.UserID)
	}

	// 2. Record warning
	warning := &domain.UserWarning{
		GuildID:        "guild_1",
		UserID:         "bad_user_1",
		AdminID:        "admin_staff_1",
		MessageContent: "I wrote badword",
		RuleMatched:    "badword",
		TicketID:       "ticket_chan_1",
	}
	if err := svc.RecordWarning(ctx, warning); err != nil {
		t.Fatalf("failed to record warning: %v", err)
	}

	// Retrieve warnings
	warnings, err := svc.GetWarnings(ctx, "guild_1", "bad_user_1")
	if err != nil {
		t.Fatalf("failed to get warnings: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].AdminID != "admin_staff_1" {
		t.Errorf("expected admin ID admin_staff_1, got %s", warnings[0].AdminID)
	}

	count, err := svc.GetWarningCount(ctx, "guild_1", "bad_user_1")
	if err != nil || count != 1 {
		t.Fatalf("expected count 1, got %d (err: %v)", count, err)
	}

	// 3. IsStaff check
	if !svc.IsStaff([]string{"role_staff_admin"}) {
		t.Fatal("expected staff role to be true")
	}
	if svc.IsStaff([]string{"role_regular"}) {
		t.Fatal("expected regular role to be false")
	}
}

