package discord

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/config"
	"syn/internal/discord/cache"
	"syn/internal/discord/handlers"
)

// Bot encapsulates the Discord session, command router, resource cache, and lifecycle management.
type Bot struct {
	session        *discordgo.Session
	cfg            *config.Config
	logger         *slog.Logger
	routes         map[string]InteractionHandlerFunc
	registeredCmds []*discordgo.ApplicationCommand
	rrHandler      *handlers.ReactionRoleHandler
	memberHandler  *handlers.MemberJoinHandler
	cache          *cache.ResourceCache
}

// NewBot creates and configures a new Discord Bot instance with security intents,
// dynamic resource caching, automatic member role management, and slash command routing.
func NewBot(
	cfg *config.Config,
	logger *slog.Logger,
	verifyHandler *handlers.VerifyHandler,
	rrHandler *handlers.ReactionRoleHandler,
	memberHandler *handlers.MemberJoinHandler,
	resCache *cache.ResourceCache,
) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}

	// IntentsGuildMessageReactions allows receiving reaction events without reading message content.
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMembers |
		discordgo.IntentsGuildMessageReactions

	b := &Bot{
		session:       session,
		cfg:           cfg,
		logger:        logger,
		routes:        make(map[string]InteractionHandlerFunc),
		rrHandler:     rrHandler,
		memberHandler: memberHandler,
		cache:         resCache,
	}

	// 1. Register /verify-member (Staff & Server Helpers Only)
	if verifyHandler != nil {
		b.RegisterCommand(
			handlers.VerifyCommandDefinition(),
			RecoverPanic(logger, RequireRoles(cfg.StaffRoleIDs, logger, verifyHandler.HandleVerifyCommand)),
		)
	}

	// 2. Register /reaction-panel (Staff & Server Helpers Only)
	if rrHandler != nil {
		b.RegisterCommand(
			handlers.ReactionRoleCommandDefinition(),
			RecoverPanic(logger, RequireRoles(cfg.StaffRoleIDs, logger, rrHandler.HandlePanelCommand)),
		)
		session.AddHandler(rrHandler.HandleReactionAdd)
		session.AddHandler(rrHandler.HandleReactionRemove)
	}

	// 3. Bind Automatic Member Join Handler (assigns Unverified role)
	if memberHandler != nil {
		session.AddHandler(memberHandler.HandleGuildMemberAdd)
	}

	// 4. Bind Real-time Resource Cache Listeners (dynamically syncs Role & Channel mutations)
	if resCache != nil {
		session.AddHandler(resCache.HandleGuildRoleCreate)
		session.AddHandler(resCache.HandleGuildRoleUpdate)
		session.AddHandler(resCache.HandleGuildRoleDelete)
		session.AddHandler(resCache.HandleChannelCreate)
		session.AddHandler(resCache.HandleChannelUpdate)
		session.AddHandler(resCache.HandleChannelDelete)
	}

	// 5. Slash Command Router & Lifecycle Events
	session.AddHandler(b.handleInteraction)

	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		logger.Info("discord websocket connected", "bot_user", r.User.Username, "guild_id", cfg.GuildID)
	})

	return b, nil
}

// Cache returns the dynamic server resource cache.
func (b *Bot) Cache() *cache.ResourceCache {
	return b.cache
}

// RegisterCommand binds a slash command definition to its middleware-wrapped handler.
func (b *Bot) RegisterCommand(cmd *discordgo.ApplicationCommand, handler InteractionHandlerFunc) {
	b.routes[cmd.Name] = handler
	b.registeredCmds = append(b.registeredCmds, cmd)
}

// Start opens the WebSocket connection to Discord, syncs guild slash commands,
// and warm-loads the dynamic resource cache.
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("opening discord websocket connection: %w", err)
	}

	// Safely resolve the bot's application ID without relying on the asynchronous Ready event
	appID, err := b.resolveAppID()
	if err != nil {
		_ = b.session.Close()
		return fmt.Errorf("resolving bot application identity: %w", err)
	}

	// Bulk-overwrite guild commands to ensure immediate registration in server
	if len(b.registeredCmds) > 0 {
		_, err := b.session.ApplicationCommandBulkOverwrite(
			appID,
			b.cfg.GuildID,
			b.registeredCmds,
		)
		if err != nil {
			var restErr *discordgo.RESTError
			if errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == 50001 {
				b.logger.Error("failed to register slash commands: missing 'applications.commands' OAuth2 scope (Discord 50001)",
					"guild_id", b.cfg.GuildID,
					"app_id", appID,
					"cause", "The bot was invited without the 'applications.commands' OAuth2 scope, or DISCORD_GUILD_ID is incorrect",
					"action_required", fmt.Sprintf(
						"Re-authorize the bot using this URL: https://discord.com/oauth2/authorize?client_id=%s&scope=bot%%20applications.commands&permissions=8",
						appID,
					),
				)
			} else {
				b.logger.Error("failed to register slash commands with guild",
					"guild_id", b.cfg.GuildID,
					"app_id", appID,
					"error", err,
				)
			}
		} else {
			b.logger.Info("slash commands synchronized with guild", "count", len(b.registeredCmds), "guild_id", b.cfg.GuildID)
		}
	}

	// Warm-load the resource cache with server roles and channels
	if b.cache != nil {
		if err := b.cache.SyncFromSession(b.session, b.cfg.GuildID); err != nil {
			b.logger.Warn("initial resource cache sync completed with warning", "error", err)
		}
	}

	// Sweep server to ensure all unassigned human members receive the unverified role
	if b.memberHandler != nil && b.cfg.UnverifiedRoleID != "" {
		go func() {
			if err := b.memberHandler.SyncUnassignedMembers(b.session, b.cfg.VerifiedRoleID); err != nil {
				b.logger.Warn("unverified member sync sweep completed with warning", "error", err)
			}
		}()
	}

	return nil
}

// resolveAppID safely extracts the application/user ID from local session state
// or falls back to querying the Discord REST API (/users/@me) if the gateway Ready packet
// has not yet been processed.
func (b *Bot) resolveAppID() (string, error) {
	if b.session.State != nil && b.session.State.User != nil && b.session.State.User.ID != "" {
		return b.session.State.User.ID, nil
	}

	u, err := b.session.User("@me")
	if err != nil {
		return "", err
	}

	if b.session.State != nil && b.session.State.User == nil {
		b.session.State.User = u
	}

	return u.ID, nil
}

// Stop cleanly closes the Discord WebSocket session during container shutdown.
func (b *Bot) Stop() error {
	b.logger.Info("closing discord websocket session")
	return b.session.Close()
}

// handleInteraction routes incoming interactions and logs structured request telemetry.
func (b *Bot) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Interaction == nil {
		return
	}

	start := time.Now()
	var userID, username string
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
		username = i.Member.User.Username
	} else if i.User != nil {
		userID = i.User.ID
		username = i.User.Username
	}

	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		cmdData := i.ApplicationCommandData()
		cmdName := cmdData.Name
		subCmd := extractSubcommandName(cmdData.Options)

		b.logger.Info("received slash command interaction",
			"interaction_id", i.ID,
			"guild_id", i.GuildID,
			"channel_id", i.ChannelID,
			"user_id", userID,
			"username", username,
			"command", cmdName,
			"subcommand", subCmd,
		)

		if handler, exists := b.routes[cmdName]; exists {
			handler(s, i)
		} else {
			b.logger.Warn("no handler registered for slash command",
				"command", cmdName,
				"interaction_id", i.ID,
			)
		}

		b.logger.Info("completed slash command interaction",
			"interaction_id", i.ID,
			"command", cmdName,
			"subcommand", subCmd,
			"user_id", userID,
			"duration_ms", time.Since(start).Milliseconds(),
		)

	case discordgo.InteractionApplicationCommandAutocomplete:
		cmdData := i.ApplicationCommandData()
		cmdName := cmdData.Name
		subCmd := extractSubcommandName(cmdData.Options)

		b.logger.Info("received autocomplete interaction",
			"interaction_id", i.ID,
			"guild_id", i.GuildID,
			"channel_id", i.ChannelID,
			"user_id", userID,
			"username", username,
			"command", cmdName,
			"subcommand", subCmd,
		)

		if cmdName == "reaction-panel" && b.rrHandler != nil {
			// Guard autocomplete with RBAC so only Staff and Server Helpers can see EC2 filenames
			RecoverPanic(b.logger, RequireRoles(b.cfg.StaffRoleIDs, b.logger, b.rrHandler.HandleAutocomplete))(s, i)
		}

		b.logger.Info("completed autocomplete interaction",
			"interaction_id", i.ID,
			"command", cmdName,
			"duration_ms", time.Since(start).Milliseconds(),
		)

	case discordgo.InteractionMessageComponent:
		compData := i.MessageComponentData()
		b.logger.Info("received component interaction",
			"interaction_id", i.ID,
			"guild_id", i.GuildID,
			"channel_id", i.ChannelID,
			"user_id", userID,
			"username", username,
			"custom_id", compData.CustomID,
			"component_type", compData.ComponentType,
		)

	case discordgo.InteractionModalSubmit:
		modalData := i.ModalSubmitData()
		b.logger.Info("received modal submit interaction",
			"interaction_id", i.ID,
			"guild_id", i.GuildID,
			"channel_id", i.ChannelID,
			"user_id", userID,
			"username", username,
			"custom_id", modalData.CustomID,
		)

	default:
		b.logger.Info("received interaction of type",
			"interaction_id", i.ID,
			"type", int(i.Type),
			"guild_id", i.GuildID,
			"user_id", userID,
		)
	}
}

func extractSubcommandName(opts []*discordgo.ApplicationCommandInteractionDataOption) string {
	if len(opts) > 0 && opts[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		return opts[0].Name
	}
	return ""
}
