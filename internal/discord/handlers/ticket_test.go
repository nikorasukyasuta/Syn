package handlers

import (
	"io"
	"log/slog"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
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

	openSub := subCommands["open"]
	typeOptFound := false
	for _, opt := range openSub.Options {
		if opt.Name == "type" {
			typeOptFound = true
			if len(opt.Choices) != 5 {
				t.Fatalf("expected 5 ticket type choices, got %d", len(opt.Choices))
			}
		}
	}
	if !typeOptFound {
		t.Fatal("expected 'type' option in /ticket open")
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
	if totalButtons != 5 {
		t.Fatalf("expected 5 buttons for the 5 ticket types, got %d", totalButtons)
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
