package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"syn/internal/config"
	"syn/internal/discord"
	"syn/internal/discord/cache"
	"syn/internal/discord/handlers"
	"syn/internal/discord/queue"
	"syn/internal/discord/syncutil"
	"syn/internal/logging"
	"syn/internal/repository/postgres"
	"syn/internal/service"
	"syn/internal/storage"
	"syn/internal/telemetry"
	"syn/internal/web"
	"syn/migrations"
)

func main() {
	// 0. Parse optional CLI flags for local development vs production mode
	devFlag := flag.Bool("dev", false, "Run bot in local development mode (forbidden on EC2 instances)")
	envFlag := flag.String("env", "", "Explicit runtime environment ('production' or 'development')")
	flag.Parse()

	if *devFlag {
		_ = os.Setenv("APP_ENV", config.EnvironmentDevelopment)
	} else if *envFlag != "" {
		_ = os.Setenv("APP_ENV", *envFlag)
	}

	// Maximize Go runtime concurrency across all available CPU cores (EC2 2-Core Instance)
	numCPUs := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPUs)

	// 1. Load and validate configuration (includes EC2 host guard for development mode)
	cfg, err := config.Load()
	if err != nil {
		fallbackLogger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		fallbackLogger.Error("configuration validation failed", "error", err)
		os.Exit(1)
	}

	// 2. Initialize Enterprise Logging Suite with Rotating File Log & Stdout Tee
	logger, logCloser, err := logging.Setup(logging.Config{
		Level:        cfg.LogLevel,
		Format:       cfg.LogFormat,
		FilePath:     cfg.LogFilePath,
		MaxSizeMB:    cfg.LogMaxSizeMB,
		MaxBackups:   cfg.LogMaxBackups,
		MaxAgeDays:   cfg.LogMaxAgeDays,
		EnableStdout: true,
		EnableFile:   true,
	})
	if err != nil {
		fallbackLogger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		fallbackLogger.Error("failed to initialize logging suite", "error", err)
		os.Exit(1)
	}
	if logCloser != nil {
		defer logCloser.Close()
	}

	logger.Info("configuration loaded successfully",
		"environment", cfg.Environment,
		"log_level", cfg.LogLevel,
		"log_file", cfg.LogFilePath,
		"guild_id", cfg.GuildID,
		"ids_file", cfg.IDsFilePath,
		"server_template_path", cfg.ServerTemplatePath,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 3. Initialize PostgreSQL Connection Pool with 30s timeout to survive container cold starts
	dbCtx, dbCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dbCancel()

	dbPool, err := postgres.NewPool(dbCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to initialize database pool", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()
	logger.Info("postgresql connection pool established")

	// Run embedded database schema migrations on startup
	if err := migrations.Run(dbCtx, dbPool, logger); err != nil {
		logger.Error("failed to apply database migrations", "error", err)
		os.Exit(1)
	}

	// 4. Wire Clean Architecture Layers (Repository -> Service -> Handlers/Cache -> Bot)
	auditRepo := postgres.NewAuditRepo(dbPool)
	verifyRepo := postgres.NewVerificationRepo(dbPool)

	// Fine-grained per-user lock manager to serialize concurrent state/role changes per user
	lockMgr := syncutil.NewUserLockManager()

	// High-throughput asynchronous command queue (worker pool sized to match host cores)
	workerCount := numCPUs * 2
	if workerCount < 4 {
		workerCount = 4
	}
	cmdQueue := queue.NewCommandQueue(1024, workerCount, logger)

	verifySvc := service.NewVerificationService(verifyRepo, auditRepo, cfg.IDVerifyReviewerRoleIDs(), logger)
	verifyHandler := handlers.NewVerifyHandler(verifySvc, cfg.VerifiedRoleID, cfg.UnverifiedRoleID, lockMgr, logger)

	assetStore, err := storage.NewLocalAssetStore(cfg.AssetsDir)
	if err != nil {
		logger.Error("failed to initialize EC2 local asset store", "error", err, "dir", cfg.AssetsDir)
		os.Exit(1)
	}

	rrRepo := postgres.NewReactionRoleRepo(dbPool)
	rrSvc := service.NewReactionRoleService(rrRepo, verifyRepo, auditRepo, assetStore, cfg.TicketStaffRoleIDs(), cfg.VerifiedRoleID, logger)
	rrSvc.AddProtectedRoles(cfg.IDVerifiedRoleID, cfg.UnverifiedRoleID)
	rrHandler := handlers.NewReactionRoleHandler(rrSvc, logger)
	rrHandler.SetLockManager(lockMgr)
	rrHandler.SetDMRoles(cfg.JoinAskToDMRoleID, cfg.AskToDMRoleID, cfg.DMsOpenRoleID, cfg.DMsClosedRoleID)

	// Dynamic Resource Cache for roles and channels
	resCache := cache.NewResourceCache(logger)

	// Automatic Member Join Handler for granting unverified role and Join "Ask To DM" role
	memberHandler := handlers.NewMemberJoinHandler(cfg.GuildID, cfg.UnverifiedRoleID, logger)
	memberHandler.SetJoinAskToDMRole(cfg.JoinAskToDMRoleID)
	memberHandler.SetLockManager(lockMgr)

	// Safeword / RED Alert Handler
	safewordHandler := handlers.NewSafewordHandler(cfg, auditRepo, logger)

	// Support & Verification Ticket System (strictly gated to Server Staff — excludes Server Helpers & ID Checkers)
	ticketRepo := postgres.NewTicketRepo(dbPool)
	ticketSvc := service.NewTicketService(ticketRepo, cfg.TicketStaffRoleIDs(), logger)
	ticketHandler := handlers.NewTicketHandler(ticketSvc, cfg, resCache, logger)

	// Health and Infrastructure Diagnostics Handler (Public nominal, Staff deep diagnostics)
	healthHandler := handlers.NewHealthHandler(cfg.TicketStaffRoleIDs(), dbPool, cmdQueue, lockMgr)

	// System Resource Telemetry Cache & Periodic JSONL Metrics Flusher (for graphing & monitoring)
	telemetryMonitor, err := telemetry.NewMonitor(
		telemetry.Config{
			FilePath:       cfg.MetricsLogPath,
			SampleInterval: time.Duration(cfg.MetricsSampleIntervalSec) * time.Second,
			FlushInterval:  time.Duration(cfg.MetricsFlushIntervalSec) * time.Second,
		},
		telemetry.NewPGXPoolStatsProvider(dbPool),
		telemetry.NewCommandQueueStatsProvider(cmdQueue),
		lockMgr,
		logger,
	)
	if err != nil {
		logger.Warn("failed to initialize system resource telemetry monitor; continuing without file flush", "error", err)
	} else {
		telemetryMonitor.Start()
		healthHandler.SetTelemetryMonitor(telemetryMonitor)
	}

	// Bot Versioning and GitHub Source Tracking Handler
	versionHandler := handlers.NewVersionHandler()

	// Automated Word Filter and Moderation Warnings
	warningRepo := postgres.NewWarningRepo(dbPool)
	filterSvc := service.NewWordFilterService(warningRepo, cfg.TicketStaffRoleIDs(), logger)
	if err := filterSvc.LoadAll(cfg.FiltersDir); err != nil {
		logger.Warn("filter service initial load completed with warning", "error", err, "dir", cfg.FiltersDir)
	}
	filterHandler := handlers.NewFilterHandler(filterSvc, ticketSvc, cfg, resCache, logger)

	// Flash and Dash Ephemeral Photo Channels (30-minute default timer)
	flashRepo := postgres.NewFlashRepo(dbPool)
	flashSvc := service.NewFlashDashService(flashRepo, cfg.TicketStaffRoleIDs(), logger)
	flashHandler := handlers.NewFlashHandler(flashSvc, cfg, logger)

	// User-Configured Spoilered Photo TTL Auto-Deletion (30m to 14d, available to all members)
	spoilerTTLRepo := postgres.NewSpoilerTTLRepo(dbPool)
	spoilerTTLSvc := service.NewSpoilerTTLService(spoilerTTLRepo, flashSvc, logger)
	spoilerTTLHandler := handlers.NewSpoilerTTLHandler(spoilerTTLSvc, cfg, logger)

	// Legal and Privacy Command Handlers
	legalHandler := handlers.NewLegalHandler(cfg)

	// Dedicated 18+ ID Verification Flow (accessible to Server Staff, Server Helpers & ID Checkers)
	idVerifyRepo := postgres.NewIDVerificationRepo(dbPool)
	idVerifySvc := service.NewIDVerificationService(idVerifyRepo, verifyRepo, auditRepo, cfg.IDVerifyReviewerRoleIDs(), logger)
	idVerifyHandler := handlers.NewIDVerifyHandler(idVerifySvc, cfg, resCache, lockMgr, logger)

	// Administrator-Only Server Template Export & Development Server Apply Suite
	templateSvc := service.NewServerTemplateService(logger)
	templateHandler := handlers.NewTemplateHandler(templateSvc, cfg, resCache, logger)

	// Public Web Server for Terms, Privacy Policy, Health, and Time-Series Metrics
	webServer := web.NewServer(cfg.WebPort, logger)
	if telemetryMonitor != nil {
		webServer.SetTelemetryMonitor(telemetryMonitor)
	}
	go func() {
		if err := webServer.Start(); err != nil {
			logger.Error("web server stopped with error", "error", err)
		}
	}()

	bot, err := discord.NewBot(
		cfg,
		logger,
		verifyHandler,
		rrHandler,
		memberHandler,
		safewordHandler,
		ticketHandler,
		healthHandler,
		versionHandler,
		filterHandler,
		flashHandler,
		legalHandler,
		cmdQueue,
		lockMgr,
		resCache,
	)
	if err != nil {
		logger.Error("failed to initialize discord bot", "error", err)
		os.Exit(1)
	}
	bot.SetIDVerifyHandler(idVerifyHandler)
	bot.SetTemplateHandler(templateHandler)
	bot.SetSpoilerTTLHandler(spoilerTTLHandler)

	// 5. Start the Bot
	if err := bot.Start(); err != nil {
		logger.Error("failed to start discord bot", "error", err)
		os.Exit(1)
	}

	// Initialize Flash and Dash service (warm load channels and resume pending deletion timers)
	if err := flashSvc.Init(ctx, bot.Session(), cfg.GuildID); err != nil {
		logger.Warn("failed to initialize flash and dash service", "error", err)
	}

	// Initialize Spoilered Photo TTL service (warm load user TTLs and resume pending deletion timers)
	if err := spoilerTTLSvc.Init(ctx, bot.Session(), cfg.GuildID); err != nil {
		logger.Warn("failed to initialize spoiler ttl service", "error", err)
	}

	// Automatically provision or sync the read-only Ticket Creation Hub channel and its per-type posts.
	// In local development mode, skip if TicketCategoryID is not yet populated (until /template-apply runs).
	if !cfg.IsDevelopment() || cfg.TicketCategoryID != "" {
		go func() {
			hubCtx, hubCancel := context.WithTimeout(ctx, 15*time.Second)
			defer hubCancel()
			if hubChID, err := ticketHandler.EnsureTicketHub(hubCtx, bot.Session(), cfg.GuildID); err != nil {
				logger.Warn("failed to auto-sync ticket creation hub on startup", "error", err)
			} else {
				logger.Info("ticket creation hub synchronized", "channel_id", hubChID)
			}
		}()
	} else {
		logger.Info("development mode: skipping startup ticket hub auto-provisioning until /template-apply populates ticket_category_id")
	}

	// 6. Block until OS termination signal (Docker stop / EC2 reboot)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	sig := <-stopChan

	logger.Info("received shutdown signal, initiating graceful shutdown", "signal", sig.String())

	// Allow up to 10 seconds for in-flight database transactions and web requests to complete
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if telemetryMonitor != nil {
		if err := telemetryMonitor.Stop(); err != nil {
			logger.ErrorContext(shutdownCtx, "error stopping telemetry monitor", "error", err)
		}
	}

	if err := webServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorContext(shutdownCtx, "error shutting down web server", "error", err)
	}

	if err := bot.Stop(); err != nil {
		logger.ErrorContext(shutdownCtx, "error closing discord session", "error", err)
	}

	logger.Info("shutdown complete")
}
