package main

import (
	"context"
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
	"syn/internal/web"
	"syn/migrations"
)

func main() {
	// 0. Maximize Go runtime concurrency across all available CPU cores (EC2 2-Core Instance)
	numCPUs := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPUs)

	// 1. Load and validate configuration
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
		"log_level", cfg.LogLevel,
		"log_file", cfg.LogFilePath,
		"guild_id", cfg.GuildID,
		"ids_file", cfg.IDsFilePath,
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

	verifySvc := service.NewVerificationService(verifyRepo, auditRepo, cfg.StaffRoleIDs, logger)
	verifyHandler := handlers.NewVerifyHandler(verifySvc, cfg.VerifiedRoleID, cfg.UnverifiedRoleID, lockMgr, logger)

	assetStore, err := storage.NewLocalAssetStore(cfg.AssetsDir)
	if err != nil {
		logger.Error("failed to initialize EC2 local asset store", "error", err, "dir", cfg.AssetsDir)
		os.Exit(1)
	}

	rrRepo := postgres.NewReactionRoleRepo(dbPool)
	rrSvc := service.NewReactionRoleService(rrRepo, verifyRepo, auditRepo, assetStore, cfg.StaffRoleIDs, cfg.VerifiedRoleID, logger)
	rrHandler := handlers.NewReactionRoleHandler(rrSvc, logger)
	rrHandler.SetLockManager(lockMgr)

	// Dynamic Resource Cache for roles and channels
	resCache := cache.NewResourceCache(logger)

	// Automatic Member Join Handler for granting unverified role
	memberHandler := handlers.NewMemberJoinHandler(cfg.GuildID, cfg.UnverifiedRoleID, logger)
	memberHandler.SetLockManager(lockMgr)

	// Safeword / RED Alert Handler
	safewordHandler := handlers.NewSafewordHandler(cfg, auditRepo, logger)

	// Support & Verification Ticket System
	ticketRepo := postgres.NewTicketRepo(dbPool)
	ticketSvc := service.NewTicketService(ticketRepo, cfg.StaffRoleIDs, logger)
	ticketHandler := handlers.NewTicketHandler(ticketSvc, cfg, resCache, logger)

	// Health and Infrastructure Diagnostics Handler (Public nominal, Staff deep diagnostics)
	healthHandler := handlers.NewHealthHandler(cfg.StaffRoleIDs, dbPool, cmdQueue, lockMgr)

	// Bot Versioning and GitHub Source Tracking Handler
	versionHandler := handlers.NewVersionHandler()

	// Automated Word Filter and Moderation Warnings
	warningRepo := postgres.NewWarningRepo(dbPool)
	filterSvc := service.NewWordFilterService(warningRepo, cfg.StaffRoleIDs, logger)
	if err := filterSvc.LoadAll(cfg.FiltersDir); err != nil {
		logger.Warn("filter service initial load completed with warning", "error", err, "dir", cfg.FiltersDir)
	}
	filterHandler := handlers.NewFilterHandler(filterSvc, ticketSvc, cfg, resCache, logger)

	// Flash and Dash Ephemeral Photo Channels
	flashRepo := postgres.NewFlashRepo(dbPool)
	flashSvc := service.NewFlashDashService(flashRepo, cfg.StaffRoleIDs, logger)
	flashHandler := handlers.NewFlashHandler(flashSvc, cfg, logger)

	// Legal and Privacy Command Handlers
	legalHandler := handlers.NewLegalHandler(cfg)

	// Public Web Server for Terms, Privacy Policy, and Health
	webServer := web.NewServer(cfg.WebPort, logger)
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

	// 5. Start the Bot
	if err := bot.Start(); err != nil {
		logger.Error("failed to start discord bot", "error", err)
		os.Exit(1)
	}

	// Initialize Flash and Dash service (warm load channels and resume pending deletion timers)
	if err := flashSvc.Init(ctx, bot.Session(), cfg.GuildID); err != nil {
		logger.Warn("failed to initialize flash and dash service", "error", err)
	}

	// 6. Block until OS termination signal (Docker stop / EC2 reboot)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	sig := <-stopChan

	logger.Info("received shutdown signal, initiating graceful shutdown", "signal", sig.String())

	// Allow up to 10 seconds for in-flight database transactions and web requests to complete
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := webServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorContext(shutdownCtx, "error shutting down web server", "error", err)
	}

	if err := bot.Stop(); err != nil {
		logger.ErrorContext(shutdownCtx, "error closing discord session", "error", err)
	}

	logger.Info("shutdown complete")
}
