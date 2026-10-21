package handlers_test

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"

	"syn/internal/discord/handlers"
)

func TestMemberJoinHandler_HandleGuildMemberAdd(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	session := newMockSession(http.StatusOK, `{}`)

	h := handlers.NewMemberJoinHandler("guild_1", "role_unverified_123", silentLogger)

	// 1. Success case: regular human user joins configured guild
	h.HandleGuildMemberAdd(session, &discordgo.GuildMemberAdd{
		Member: &discordgo.Member{
			GuildID: "guild_1",
			User: &discordgo.User{
				ID:       "user_human",
				Username: "newbie",
				Bot:      false,
			},
		},
	})

	// 2. Bot account joins -> should be ignored
	h.HandleGuildMemberAdd(session, &discordgo.GuildMemberAdd{
		Member: &discordgo.Member{
			GuildID: "guild_1",
			User: &discordgo.User{
				ID:       "bot_app",
				Username: "helper_bot",
				Bot:      true,
			},
		},
	})

	// 3. Different guild joins -> should be ignored
	h.HandleGuildMemberAdd(session, &discordgo.GuildMemberAdd{
		Member: &discordgo.Member{
			GuildID: "other_guild",
			User: &discordgo.User{
				ID:       "user_other",
				Username: "stranger",
				Bot:      false,
			},
		},
	})

	// 4. Missing unverified role ID -> graceful return without crash
	hNoRole := handlers.NewMemberJoinHandler("guild_1", "", silentLogger)
	hNoRole.HandleGuildMemberAdd(session, &discordgo.GuildMemberAdd{
		Member: &discordgo.Member{
			GuildID: "guild_1",
			User: &discordgo.User{
				ID:  "user_human_2",
				Bot: false,
			},
		},
	})

	// 5. Nil check
	h.HandleGuildMemberAdd(session, nil)
}

