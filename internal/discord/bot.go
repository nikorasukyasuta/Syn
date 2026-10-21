package discord

import (
	"fmt"
	"log/slog"

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
		session:   session,
		cfg:       cfg,
		logger:    logger,
		routes:    make(map[string]InteractionHandlerFunc),
		rrHandler: rrHandler,
		cache:     resCache,
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

	// Bulk-overwrite guild commands to ensure immediate registration in server
	_, err := b.session.ApplicationCommandBulkOverwrite(
		b.session.State.User.ID,
		b.cfg.GuildID,
		b.registeredCmds,
	)
	if err != nil {
		_ = b.session.Close()
		return fmt.Errorf("registering slash commands with guild %s: %w", b.cfg.GuildID, err)
	}

	b.logger.Info("slash commands synchronized with guild", "count", len(b.registeredCmds))

	// Warm-load the resource cache with server roles and channels
	if b.cache != nil {
		if err := b.cache.SyncFromSession(b.session, b.cfg.GuildID); err != nil {
			b.logger.Warn("initial resource cache sync completed with warning", "error", err)
		}
	}

	return nil
}

// Stop cleanly closes the Discord WebSocket session during container shutdown.
func (b *Bot) Stop() error {
	b.logger.Info("closing discord websocket session")
	return b.session.Close()
}

// handleInteraction routes incoming ApplicationCommands to their registered handler.
func (b *Bot) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		cmdName := i.ApplicationCommandData().Name
		if handler, exists := b.routes[cmdName]; exists {
			handler(s, i)
		}
	case discordgo.InteractionApplicationCommandAutocomplete:
		if i.ApplicationCommandData().Name == "reaction-panel" {
			// Guard autocomplete with RBAC so only Staff and Server Helpers can see EC2 filenames
			RecoverPanic(b.logger, RequireRoles(b.cfg.StaffRoleIDs, b.logger, b.rrHandler.HandleAutocomplete))(s, i)
		}
	}
}
