package cache_test

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/discord/cache"
)

func TestResourceCache_RoleCRUD(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	c := cache.NewResourceCache(silentLogger)

	// 1. Initial Set
	c.SetRole("role_1", "18+ Verified")

	name, ok := c.GetRoleName("role_1")
	if !ok || name != "18+ Verified" {
		t.Fatalf("expected '18+ Verified', got %q (ok: %t)", name, ok)
	}

	id, ok := c.GetRoleID("18+ verified") // test case-insensitivity
	if !ok || id != "role_1" {
		t.Fatalf("expected 'role_1', got %q (ok: %t)", id, ok)
	}

	// 2. Rename role
	c.SetRole("role_1", "18+ Age Verified")

	// Old name should no longer resolve
	_, ok = c.GetRoleID("18+ Verified")
	if ok {
		t.Fatal("expected old role name to be cleared")
	}

	// New name resolves
	id, ok = c.GetRoleID("18+ age verified")
	if !ok || id != "role_1" {
		t.Fatalf("expected 'role_1', got %q", id)
	}

	// 3. Delete role
	c.RemoveRole("role_1")
	_, ok = c.GetRoleName("role_1")
	if ok {
		t.Fatal("expected role_1 to be removed")
	}
	_, ok = c.GetRoleID("18+ age verified")
	if ok {
		t.Fatal("expected name mapping to be removed")
	}
}

func TestResourceCache_ChannelCRUD(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	c := cache.NewResourceCache(silentLogger)

	c.SetChannel("chan_1", "general-chat")

	name, ok := c.GetChannelName("chan_1")
	if !ok || name != "general-chat" {
		t.Fatalf("expected 'general-chat', got %q", name)
	}

	id, ok := c.GetChannelID("GENERAL-CHAT")
	if !ok || id != "chan_1" {
		t.Fatalf("expected 'chan_1', got %q", id)
	}

	// Rename channel
	c.SetChannel("chan_1", "general-lounge")
	_, ok = c.GetChannelID("general-chat")
	if ok {
		t.Fatal("expected old channel name to be removed")
	}

	id, ok = c.GetChannelID("general-lounge")
	if !ok || id != "chan_1" {
		t.Fatalf("expected 'chan_1', got %q", id)
	}

	c.RemoveChannel("chan_1")
	_, ok = c.GetChannelName("chan_1")
	if ok {
		t.Fatal("expected chan_1 to be removed")
	}
}

func TestResourceCache_CompareAndResolve(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	c := cache.NewResourceCache(silentLogger)

	c.SetRole("r100", "Server Helper")
	c.SetChannel("c200", "verification-logs")

	// CompareRole
	actual, matches := c.CompareRole("r100", "server helper")
	if !matches || actual != "Server Helper" {
		t.Fatalf("expected match for Server Helper, got %q (matches: %t)", actual, matches)
	}

	_, matches = c.CompareRole("r100", "Admin")
	if matches {
		t.Fatal("expected mismatch for Admin")
	}

	_, matches = c.CompareRole("r_unknown", "Admin")
	if matches {
		t.Fatal("expected mismatch for non-existent role")
	}

	// CompareChannel
	actual, matches = c.CompareChannel("c200", "VERIFICATION-LOGS")
	if !matches || actual != "verification-logs" {
		t.Fatalf("expected match for verification-logs, got %q (matches: %t)", actual, matches)
	}

	// ResolveRole by ID
	id, name, found := c.ResolveRole("r100")
	if !found || id != "r100" || name != "Server Helper" {
		t.Fatalf("ResolveRole by ID failed: id=%q name=%q found=%t", id, name, found)
	}

	// ResolveRole by Name
	id, name, found = c.ResolveRole("SERVER HELPER")
	if !found || id != "r100" || name != "Server Helper" {
		t.Fatalf("ResolveRole by Name failed: id=%q name=%q found=%t", id, name, found)
	}

	// ResolveRole non-existent
	_, _, found = c.ResolveRole("non_existent")
	if found {
		t.Fatal("expected non-existent role not to be found")
	}

	// ResolveChannel by ID and by Name
	id, name, found = c.ResolveChannel("c200")
	if !found || id != "c200" || name != "verification-logs" {
		t.Fatalf("ResolveChannel by ID failed: id=%q name=%q found=%t", id, name, found)
	}

	id, name, found = c.ResolveChannel("verification-logs")
	if !found || id != "c200" || name != "verification-logs" {
		t.Fatalf("ResolveChannel by Name failed: id=%q name=%q found=%t", id, name, found)
	}
}

func TestResourceCache_SyncAndGetAll(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	c := cache.NewResourceCache(silentLogger)

	guild := &discordgo.Guild{
		Roles: []*discordgo.Role{
			{ID: "r1", Name: "Staff"},
			{ID: "r2", Name: "Member"},
		},
		Channels: []*discordgo.Channel{
			{ID: "c1", Name: "welcome"},
			{ID: "c2", Name: "rules"},
		},
	}

	c.SyncGuild(guild)

	roles := c.GetAllRoles()
	if len(roles) != 2 || roles["r1"] != "Staff" || roles["r2"] != "Member" {
		t.Fatalf("unexpected roles snapshot: %v", roles)
	}

	channels := c.GetAllChannels()
	if len(channels) != 2 || channels["c1"] != "welcome" || channels["c2"] != "rules" {
		t.Fatalf("unexpected channels snapshot: %v", channels)
	}
}

func TestResourceCache_EventHandlers(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	c := cache.NewResourceCache(silentLogger)

	// Role create
	c.HandleGuildRoleCreate(nil, &discordgo.GuildRoleCreate{
		GuildRole: &discordgo.GuildRole{
			Role: &discordgo.Role{ID: "r_evt", Name: "EventRole"},
		},
	})
	if name, _ := c.GetRoleName("r_evt"); name != "EventRole" {
		t.Fatalf("expected EventRole, got %s", name)
	}

	// Role update
	c.HandleGuildRoleUpdate(nil, &discordgo.GuildRoleUpdate{
		GuildRole: &discordgo.GuildRole{
			Role: &discordgo.Role{ID: "r_evt", Name: "RenamedEventRole"},
		},
	})
	if name, _ := c.GetRoleName("r_evt"); name != "RenamedEventRole" {
		t.Fatalf("expected RenamedEventRole, got %s", name)
	}

	// Role delete
	c.HandleGuildRoleDelete(nil, &discordgo.GuildRoleDelete{
		RoleID: "r_evt",
	})
	if _, ok := c.GetRoleName("r_evt"); ok {
		t.Fatal("expected r_evt to be deleted")
	}

	// Channel create
	c.HandleChannelCreate(nil, &discordgo.ChannelCreate{
		Channel: &discordgo.Channel{ID: "c_evt", Name: "bot-commands"},
	})
	if name, _ := c.GetChannelName("c_evt"); name != "bot-commands" {
		t.Fatalf("expected bot-commands, got %s", name)
	}

	// Channel update
	c.HandleChannelUpdate(nil, &discordgo.ChannelUpdate{
		Channel: &discordgo.Channel{ID: "c_evt", Name: "bot-testing"},
	})
	if name, _ := c.GetChannelName("c_evt"); name != "bot-testing" {
		t.Fatalf("expected bot-testing, got %s", name)
	}

	// Channel delete
	c.HandleChannelDelete(nil, &discordgo.ChannelDelete{
		Channel: &discordgo.Channel{ID: "c_evt"},
	})
	if _, ok := c.GetChannelName("c_evt"); ok {
		t.Fatal("expected c_evt to be deleted")
	}
}

func TestResourceCache_ConcurrentSafety(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	c := cache.NewResourceCache(silentLogger)

	var wg sync.WaitGroup
	workers := 10
	iterations := 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				roleID := fmt.Sprintf("role_%d_%d", id, j)
				roleName := fmt.Sprintf("Role-%d-%d", id, j)
				c.SetRole(roleID, roleName)
				_, _ = c.GetRoleName(roleID)
				_, _ = c.GetRoleID(roleName)
				_, _, _ = c.ResolveRole(roleName)
				_, _ = c.CompareRole(roleID, roleName)

				chanID := fmt.Sprintf("chan_%d_%d", id, j)
				chanName := fmt.Sprintf("chan-%d-%d", id, j)
				c.SetChannel(chanID, chanName)
				_, _ = c.GetChannelName(chanID)
				_, _ = c.GetChannelID(chanName)
				_, _, _ = c.ResolveChannel(chanName)
			}
		}(i)
	}

	wg.Wait()
}
