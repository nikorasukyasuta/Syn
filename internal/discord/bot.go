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
	"syn/internal/discord/queue"
	"syn/internal/discord/ratelimit"
	"syn/internal/discord/syncutil"
)

// Bot encapsulates the Discord session, command router, resource cache, and lifecycle management.
type Bot struct {
	session         *discordgo.Session
	cfg             *config.Config
	logger          *slog.Logger
	routes          map[string]InteractionHandlerFunc
	registeredCmds  []*discordgo.ApplicationCommand
	rrHandler       *handlers.ReactionRoleHandler
	memberHandler   *handlers.MemberJoinHandler
	safewordHandler *handlers.SafewordHandler
	ticketHandler   *handlers.TicketHandler
	healthHandler   *handlers.HealthHandler
	versionHandler  *handlers.VersionHandler
	cmdQueue        *queue.CommandQueue
	lockMgr         *syncutil.UserLockManager
	rateLimiter     *ratelimit.UserRateLimiter
	cache           *cache.ResourceCache
}

// NewBot creates and configures a new Discord Bot instance with security intents,
// dynamic resource caching, automatic member role management, and slash command routing.
func NewBot(
	cfg *config.Config,
	logger *slog.Logger,
	verifyHandler *handlers.VerifyHandler,
	rrHandler *handlers.ReactionRoleHandler,
	memberHandler *handlers.MemberJoinHandler,
	safewordHandler *handlers.SafewordHandler,
	ticketHandler *handlers.TicketHandler,
	healthHandler *handlers.HealthHandler,
	versionHandler *handlers.VersionHandler,
	cmdQueue *queue.CommandQueue,
	lockMgr *syncutil.UserLockManager,
	resCache *cache.ResourceCache,
) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}

	// Configure HTTP client timeouts and retry backoff to avoid hanging connections
	session.Client.Timeout = 15 * time.Second
	session.MaxRestRetries = 3

	// IntentsGuildMessageReactions allows receiving reaction events without reading message content.
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMembers |
		discordgo.IntentsGuildMessageReactions

	// In-memory token bucket rate limiter: burst of 5 commands, refilling 1 per second
	rateLimiter := ratelimit.NewUserRateLimiter(5, time.Second, 5*time.Minute)

	b := &Bot{
		session:         session,
		cfg:             cfg,
		logger:          logger,
		routes:          make(map[string]InteractionHandlerFunc),
		rrHandler:       rrHandler,
		memberHandler:   memberHandler,
		safewordHandler: safewordHandler,
		ticketHandler:   ticketHandler,
		healthHandler:   healthHandler,
		versionHandler:  versionHandler,
		cmdQueue:        cmdQueue,
		lockMgr:         lockMgr,
		rateLimiter:     rateLimiter,
		cache:           resCache,
	}

	// 1. Register /verify-member (Staff & Server Helpers Only)
	if verifyHandler != nil {
		b.RegisterCommand(
			handlers.VerifyCommandDefinition(),
			b.wrapHandler(verifyHandler.HandleVerifyCommand, true, false),
		)
	}

	// 2. Register /reaction-panel (Staff & Server Helpers Only)
	if rrHandler != nil {
		b.RegisterCommand(
			handlers.ReactionRoleCommandDefinition(),
			b.wrapHandler(rrHandler.HandlePanelCommand, true, false),
		)
		session.AddHandler(rrHandler.HandleReactionAdd)
		session.AddHandler(rrHandler.HandleReactionRemove)
	}

	// 3. Bind Automatic Member Join Handler (assigns Unverified role)
	if memberHandler != nil {
		session.AddHandler(memberHandler.HandleGuildMemberAdd)
	}

	// 4. Register Safeword / RED Alert Handlers
	if safewordHandler != nil {
		// /red is public to ALL server members and exempt from command rate limiting
		b.RegisterCommand(
			handlers.RedCommandDefinition(),
			b.wrapHandler(safewordHandler.HandleRedCommand, false, true),
		)
		// /clear-slowmode is Staff & Server Helpers Only
		b.RegisterCommand(
			handlers.ClearSlowmodeCommandDefinition(),
			b.wrapHandler(safewordHandler.HandleClearSlowmodeCommand, true, false),
		)
		// Listen for 🛑 reaction trigger on any message
		session.AddHandler(safewordHandler.HandleReactionAdd)
	}

	// 5. Register /ticket (All Users for open/close; Staff for panel)
	if ticketHandler != nil {
		b.RegisterCommand(
			handlers.TicketCommandDefinition(),
			b.wrapHandler(ticketHandler.HandleTicketCommand, false, false),
		)
	}

	// 6. Register /health (Public nominal status; Staff full infrastructure diagnostics)
	if healthHandler != nil {
		b.RegisterCommand(
			handlers.HealthCommandDefinition(),
			b.wrapHandler(healthHandler.HandleHealthCommand, false, false),
		)
	}

	// 7. Register /version (Public bot version, update changelog, and GitHub source link)
	if versionHandler != nil {
		b.RegisterCommand(
			handlers.VersionCommandDefinition(),
			b.wrapHandler(versionHandler.HandleVersionCommand, false, false),
		)
	}

	// 8. Bind Real-time Resource Cache Listeners (dynamically syncs Role & Channel mutations)
	if resCache != nil {
		session.AddHandler(resCache.HandleGuildRoleCreate)
		session.AddHandler(resCache.HandleGuildRoleUpdate)
		session.AddHandler(resCache.HandleGuildRoleDelete)
		session.AddHandler(resCache.HandleChannelCreate)
		session.AddHandler(resCache.HandleChannelUpdate)
		session.AddHandler(resCache.HandleChannelDelete)
	}

	// 7. Slash Command Router & Lifecycle Events
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
	if b.cmdQueue != nil {
		b.cmdQueue.Stop(5 * time.Second)
	}
	if b.rateLimiter != nil {
		b.rateLimiter.Stop()
	}
	return b.session.Close()
}

// wrapHandler constructs an enterprise middleware pipeline for slash commands:
// 1. RecoverPanic: Catches runtime panics gracefully
// 2. RateLimit: Protects bot from user command flood and prevents Discord API rate limit starvation
// 3. WithTimeoutGuard: Ensures slow commands auto-defer before 1.5s, preventing Discord 3s timeouts
// 4. RequireRoles: Enforces staff role authorization when required
func (b *Bot) wrapHandler(handler InteractionHandlerFunc, staffOnly bool, exemptFromRateLimit bool) InteractionHandlerFunc {
	h := handler
	if staffOnly {
		h = RequireRoles(b.cfg.StaffRoleIDs, b.logger, h)
	}
	h = WithTimeoutGuard(1200*time.Millisecond, h)

	var exempt []string
	if exemptFromRateLimit {
		exempt = []string{"red"}
	}
	h = RateLimit(b.rateLimiter, b.cfg.StaffRoleIDs, exempt, b.logger, h)
	return RecoverPanic(b.logger, h)
}

// isStaffMember checks if the interaction author holds any configured staff or helper role.
func (b *Bot) isStaffMember(i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	staffMap := make(map[string]struct{}, len(b.cfg.StaffRoleIDs))
	for _, id := range b.cfg.StaffRoleIDs {
		staffMap[id] = struct{}{}
	}
	return HasAnyRole(i.Member.Roles, staffMap)
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
			if b.cmdQueue != nil {
				enqueued := b.cmdQueue.Enqueue(&queue.Task{
					ID:          i.ID,
					CommandName: cmdName,
					UserID:      userID,
					Execute: func() {
						handler(s, i)
						b.logger.Info("completed slash command interaction",
							"interaction_id", i.ID,
							"command", cmdName,
							"subcommand", subCmd,
							"user_id", userID,
							"duration_ms", time.Since(start).Milliseconds(),
						)
					},
				})
				if !enqueued {
					b.logger.Error("failed to enqueue slash command interaction, falling back to direct execution",
						"interaction_id", i.ID,
						"command", cmdName,
					)
					handler(s, i)
					b.logger.Info("completed slash command interaction",
						"interaction_id", i.ID,
						"command", cmdName,
						"subcommand", subCmd,
						"user_id", userID,
						"duration_ms", time.Since(start).Milliseconds(),
					)
				}
			} else {
				handler(s, i)
				b.logger.Info("completed slash command interaction",
					"interaction_id", i.ID,
					"command", cmdName,
					"subcommand", subCmd,
					"user_id", userID,
					"duration_ms", time.Since(start).Milliseconds(),
				)
			}
		} else {
			b.logger.Warn("no handler registered for slash command",
				"command", cmdName,
				"interaction_id", i.ID,
			)
		}

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

		// Guard button clicks from rapid user spam to prevent dropped responses
		if userID != "" && b.rateLimiter != nil && !b.isStaffMember(i) {
			if allowed, wait := b.rateLimiter.Allow(userID); !allowed {
				waitSec := int(wait.Seconds())
				if waitSec < 1 {
					waitSec = 1
				}
				b.logger.Warn("button interaction rate limited",
					"user_id", userID,
					"custom_id", compData.CustomID,
					"retry_after_sec", waitSec,
				)
				respondForbidden(s, i, fmt.Sprintf("⏱️ **Slow down!** You are clicking buttons too quickly. Please wait %d second(s).", waitSec))
				break
			}
		}

		if b.ticketHandler != nil {
			RecoverPanic(b.logger, b.ticketHandler.HandleComponentInteraction)(s, i)
		}

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
		if b.ticketHandler != nil {
			RecoverPanic(b.logger, b.ticketHandler.HandleModalSubmit)(s, i)
		}

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
