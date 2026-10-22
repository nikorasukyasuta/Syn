package handlers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/handlers"
)

type mockAuditRepo struct {
	loggedActions []string
}

func (m *mockAuditRepo) LogAudit(_ context.Context, _, _, action string) error {
	m.loggedActions = append(m.loggedActions, action)
	return nil
}

func TestRedCommandDefinition(t *testing.T) {
	t.Parallel()

	cmd := handlers.RedCommandDefinition()
	if cmd.Name != "red" {
		t.Fatalf("expected command name 'red', got: %s", cmd.Name)
	}
	if cmd.DMPermission == nil || *cmd.DMPermission != false {
		t.Fatal("expected DMPermission to be false")
	}
	// /red must be available to ALL users (DefaultMemberPermissions should be nil)
	if cmd.DefaultMemberPermissions != nil {
		t.Fatal("expected DefaultMemberPermissions to be nil for /red so any member can use it")
	}
}

func TestClearSlowmodeCommandDefinition(t *testing.T) {
	t.Parallel()

	cmd := handlers.ClearSlowmodeCommandDefinition()
	if cmd.Name != "clear-slowmode" {
		t.Fatalf("expected command name 'clear-slowmode', got: %s", cmd.Name)
	}
	if cmd.DMPermission == nil || *cmd.DMPermission != false {
		t.Fatal("expected DMPermission to be false")
	}
	// /clear-slowmode must be restricted
	if cmd.DefaultMemberPermissions == nil || *cmd.DefaultMemberPermissions != 0 {
		t.Fatal("expected DefaultMemberPermissions to be 0 for /clear-slowmode")
	}
}

func TestSafewordHandler_TriggerRed_Success(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		StaffRoleIDs:        []string{"role_staff_1"},
		StaffAlertChannelID: "channel_staff_alerts",
		SafewordEmoji:       "🛑",
	}

	session := newMockSession(http.StatusOK, `{"id": "msg_red_123"}`)
	h := handlers.NewSafewordHandler(cfg, nil, silentLogger)

	// 1. Trigger RED
	msg, err := h.TriggerRed(session, "guild_1", "channel_general", "user_123", "slash_command", "feeling overwhelmed")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if msg == nil || msg.ID != "msg_red_123" {
		t.Fatalf("expected message ID msg_red_123, got: %v", msg)
	}

	// 2. Debounce: Second trigger within 15 seconds should be safely debounced
	msg2, err2 := h.TriggerRed(session, "guild_1", "channel_general", "user_456", "slash_command", "")
	if err2 != nil {
		t.Fatalf("expected nil error on debounce, got: %v", err2)
	}
	if msg2 != nil {
		t.Fatalf("expected nil message on debounced trigger, got: %v", msg2)
	}
}

func TestSafewordHandler_TriggerRed_NoStaffChannel(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		StaffRoleIDs:        []string{"role_staff_1", "role_helper_1"},
		StaffAlertChannelID: "", // In-channel alert mode
		SafewordEmoji:       "🛑",
	}

	session := newMockSession(http.StatusOK, `{"id": "msg_red_456"}`)
	h := handlers.NewSafewordHandler(cfg, nil, silentLogger)

	msg, err := h.TriggerRed(session, "guild_1", "channel_chat", "user_789", "reaction_🛑", "")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if msg == nil || msg.ID != "msg_red_456" {
		t.Fatalf("expected message ID msg_red_456, got: %v", msg)
	}
}

func TestSafewordHandler_ClearSlowmode(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		StaffRoleIDs: []string{"role_staff_1"},
	}

	session := newMockSession(http.StatusOK, `{}`)
	h := handlers.NewSafewordHandler(cfg, nil, silentLogger)

	err := h.ClearSlowmode(session, "guild_1", "channel_chat", "staff_user_1")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestSafewordHandler_ReactionAdd(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		StaffRoleIDs:        []string{"role_staff_1"},
		StaffAlertChannelID: "channel_staff_alerts",
		SafewordEmoji:       "🛑",
	}

	session := newMockSession(http.StatusOK, `{"id": "msg_red_rxn"}`)
	session.State.User = &discordgo.User{ID: "bot_1"}
	h := handlers.NewSafewordHandler(cfg, nil, silentLogger)

	// 1. Reaction from DM -> ignored
	h.HandleReactionAdd(session, &discordgo.MessageReactionAdd{
		MessageReaction: &discordgo.MessageReaction{
			GuildID: "",
			UserID:  "user_1",
			Emoji:   discordgo.Emoji{Name: "🛑"},
		},
	})

	// 2. Reaction from Bot itself -> ignored
	h.HandleReactionAdd(session, &discordgo.MessageReactionAdd{
		MessageReaction: &discordgo.MessageReaction{
			GuildID: "guild_1",
			UserID:  "bot_1",
			Emoji:   discordgo.Emoji{Name: "🛑"},
		},
	})

	// 3. Different emoji (e.g. 👍) -> ignored
	h.HandleReactionAdd(session, &discordgo.MessageReactionAdd{
		MessageReaction: &discordgo.MessageReaction{
			GuildID:   "guild_1",
			ChannelID: "channel_1",
			MessageID: "msg_norm",
			UserID:    "user_1",
			Emoji:     discordgo.Emoji{Name: "👍"},
		},
	})

	// 4. Valid stop sign reaction 🛑 -> triggers RED
	h.HandleReactionAdd(session, &discordgo.MessageReactionAdd{
		MessageReaction: &discordgo.MessageReaction{
			GuildID:   "guild_1",
			ChannelID: "channel_2",
			MessageID: "msg_target_trigger",
			UserID:    "user_safeword_caller",
			Emoji:     discordgo.Emoji{Name: "🛑"},
		},
	})
}

func TestSafewordHandler_SlashCommands(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := &config.Config{
		StaffRoleIDs:        []string{"role_staff_1"},
		StaffAlertChannelID: "channel_staff_alerts",
		SafewordEmoji:       "🛑",
	}

	session := newMockSession(http.StatusOK, `{"id": "msg_slash_red"}`)
	h := handlers.NewSafewordHandler(cfg, nil, silentLogger)

	// 1. /red slash command
	redInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_red",
			Token:     "tok_red",
			Type:      discordgo.InteractionApplicationCommand,
			GuildID:   "guild_1",
			ChannelID: "channel_10",
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "member_caller", Username: "member_caller"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "red",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "reason",
						Type:  discordgo.ApplicationCommandOptionString,
						Value: "please stop this topic",
					},
				},
			},
		},
	}
	h.HandleRedCommand(session, redInteraction)

	// 2. /clear-slowmode slash command
	clearInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:        "int_clear",
			Token:     "tok_clear",
			Type:      discordgo.InteractionApplicationCommand,
			GuildID:   "guild_1",
			ChannelID: "channel_10",
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "staff_member", Username: "staff_member"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "clear-slowmode",
			},
		},
	}
	h.HandleClearSlowmodeCommand(session, clearInteraction)
}