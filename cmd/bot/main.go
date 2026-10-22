package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"syn/internal/config"
	"syn/internal/discord"
	"syn/internal/discord/cache"
	"syn/internal/discord/handlers"
	"syn/internal/logging"
	"syn/internal/repository/postgres"
	"syn/internal/service"
	"syn/internal/storage"
	"syn/migrations"
)

func main() {
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
	verifySvc := service.NewVerificationService(verifyRepo, auditRepo, cfg.StaffRoleIDs, logger)
	verifyHandler := handlers.NewVerifyHandler(verifySvc, cfg.VerifiedRoleID, cfg.UnverifiedRoleID, logger)

	assetStore, err := storage.NewLocalAssetStore(cfg.AssetsDir)
	if err != nil {
		logger.Error("failed to initialize EC2 local asset store", "error", err, "dir", cfg.AssetsDir)
		os.Exit(1)
	}

	rrRepo := postgres.NewReactionRoleRepo(dbPool)
	rrSvc := service.NewReactionRoleService(rrRepo, verifyRepo, auditRepo, assetStore, cfg.StaffRoleIDs, cfg.VerifiedRoleID, logger)
	rrHandler := handlers.NewReactionRoleHandler(rrSvc, logger)

	// Dynamic Resource Cache for roles and channels
	resCache := cache.NewResourceCache(logger)

	// Automatic Member Join Handler for granting unverified role
	memberHandler := handlers.NewMemberJoinHandler(cfg.GuildID, cfg.UnverifiedRoleID, logger)

	// Safeword / RED Alert Handler
	safewordHandler := handlers.NewSafewordHandler(cfg, auditRepo, logger)

	// Support & Verification Ticket System
	ticketRepo := postgres.NewTicketRepo(dbPool)
	ticketSvc := service.NewTicketService(ticketRepo, cfg.StaffRoleIDs, logger)
	ticketHandler := handlers.NewTicketHandler(ticketSvc, cfg, resCache, logger)

	bot, err := discord.NewBot(cfg, logger, verifyHandler, rrHandler, memberHandler, safewordHandler, ticketHandler, resCache)
	if err != nil {
		logger.Error("failed to initialize discord bot", "error", err)
		os.Exit(1)
	}

	// 5. Start the Bot
	if err := bot.Start(); err != nil {
		logger.Error("failed to start discord bot", "error", err)
		os.Exit(1)
	}

	// 6. Block until OS termination signal (Docker stop / EC2 reboot)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)
	sig := <-stopChan

	logger.Info("received shutdown signal, initiating graceful shutdown", "signal", sig.String())

	// Allow up to 10 seconds for in-flight database transactions to complete
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := bot.Stop(); err != nil {
		logger.ErrorContext(shutdownCtx, "error closing discord session", "error", err)
	}

	logger.Info("shutdown complete")
}
