package handlers_test

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/discord/handlers"
	"syn/internal/discord/queue"
	"syn/internal/discord/syncutil"
	"syn/internal/version"
)

func TestHealthCommandDefinition(t *testing.T) {
	t.Parallel()

	cmd := handlers.HealthCommandDefinition()
	if cmd.Name != "health" {
		t.Fatalf("expected command name 'health', got %s", cmd.Name)
	}
	if cmd.DMPermission == nil || *cmd.DMPermission != false {
		t.Fatal("expected DMPermission to be false")
	}
}

func TestHealthHandler_HandleHealthCommand_Public(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	staffRoleIDs := []string{"role_staff_admin", "role_staff_mod"}
	cmdQueue := queue.NewCommandQueue(10, 2, silentLogger)
	defer cmdQueue.Stop(time.Second)
	lockMgr := syncutil.NewUserLockManager()

	h := handlers.NewHealthHandler(staffRoleIDs, nil, cmdQueue, lockMgr)
	session := newMockSession(http.StatusOK, `{}`)

	// Regular user without staff role
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_health_public",
			Token: "tok_health_pub",
			Type:  discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "user_regular_123"},
				Roles: []string{"role_regular_member"},
			},
		},
	}

	h.HandleHealthCommand(session, interaction)
}

func TestHealthHandler_HandleHealthCommand_Staff(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	staffRoleIDs := []string{"role_staff_admin", "role_staff_mod"}
	cmdQueue := queue.NewCommandQueue(10, 2, silentLogger)
	defer cmdQueue.Stop(time.Second)
	lockMgr := syncutil.NewUserLockManager()

	h := handlers.NewHealthHandler(staffRoleIDs, nil, cmdQueue, lockMgr)
	session := newMockSession(http.StatusOK, `{}`)

	// Staff user
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_health_staff",
			Token: "tok_health_stf",
			Type:  discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{
				User:  &discordgo.User{ID: "user_staff_456"},
				Roles: []string{"role_staff_admin"},
			},
		},
	}

	h.HandleHealthCommand(session, interaction)
}

func TestHealthHandler_HandleHealthCommand_NilMember(t *testing.T) {
	t.Parallel()

	h := handlers.NewHealthHandler([]string{"role_admin"}, nil, nil, nil)
	session := newMockSession(http.StatusOK, `{}`)

	// Interaction without Member (e.g. DM or minimal context)
	interaction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "int_health_nil",
			Token: "tok_health_nil",
			Type:  discordgo.InteractionApplicationCommand,
		},
	}

	h.HandleHealthCommand(session, interaction)
}

func TestFormatDurationAndHelpers(t *testing.T) {
	t.Parallel()

	// Verify version string is present in version package
	if version.CurrentVersion == "" {
		t.Fatal("expected non-empty CurrentVersion")
	}

	// Lock manager integration test
	lockMgr := syncutil.NewUserLockManager()
	unlock := lockMgr.Lock("user_test_1")
	if count := lockMgr.ActiveLocksCount(); count != 1 {
		t.Errorf("expected 1 active lock, got %d", count)
	}
	unlock()
	if count := lockMgr.ActiveLocksCount(); count != 0 {
		t.Errorf("expected 0 active locks, got %d", count)
	}
}

