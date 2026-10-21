package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds enterprise application settings, database credentials, RBAC role mappings, and logging parameters.
type Config struct {
	DiscordToken     string
	GuildID          string
	VerifiedRoleID   string
	UnverifiedRoleID string
	StaffRoleIDs     []string // Holds both Staff and Server Helper Role IDs
	DatabaseURL      string   // Full PostgreSQL connection string
	AssetsDir        string
	LogLevel         string
	LogFilePath      string
	LogMaxSizeMB     int
	LogMaxBackups    int
	LogMaxAgeDays    int
	LogFormat        string
}

// Load parses environment variables and validates required enterprise parameters.
func Load() (*Config, error) {
	rawRoles := os.Getenv("ALLOWED_VERIFIER_ROLE_IDS")
	var staffRoles []string
	for _, r := range strings.Split(rawRoles, ",") {
		if trimmed := strings.TrimSpace(r); trimmed != "" {
			staffRoles = append(staffRoles, trimmed)
		}
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		// Construct from individual variables if DATABASE_URL isn't explicitly provided
		dbUser := os.Getenv("DB_USER")
		dbPass := os.Getenv("DB_PASSWORD")
		dbHost := os.Getenv("DB_HOST")
		dbPort := os.Getenv("DB_PORT")
		dbName := os.Getenv("DB_NAME")
		dbSSL := os.Getenv("DB_SSLMODE")
		if dbSSL == "" {
			dbSSL = "disable" // Internal Docker network default; use "verify-full" for AWS RDS
		}
		if dbUser != "" && dbHost != "" && dbName != "" {
			dbURL = fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
				dbUser, dbPass, dbHost, dbPort, dbName, dbSSL)
		}
	}

	assetsDir := os.Getenv("ASSETS_DIR")
	if assetsDir == "" {
		assetsDir = "/app/assets/panels"
	}

	logFilePath := os.Getenv("LOG_FILE_PATH")
	if logFilePath == "" {
		logFilePath = "logs/syn.log"
	}

	logMaxSize := parseIntEnv("LOG_MAX_SIZE_MB", 10)
	logMaxBackups := parseIntEnv("LOG_MAX_BACKUPS", 5)
	logMaxAge := parseIntEnv("LOG_MAX_AGE_DAYS", 30)

	logFormat := os.Getenv("LOG_FORMAT")
	if logFormat == "" {
		logFormat = "json"
	}

	cfg := &Config{
		DiscordToken:     os.Getenv("DISCORD_TOKEN"),
		GuildID:          os.Getenv("DISCORD_GUILD_ID"),
		VerifiedRoleID:   os.Getenv("DISCORD_VERIFIED_ROLE_ID"),
		UnverifiedRoleID: os.Getenv("DISCORD_UNVERIFIED_ROLE_ID"),
		StaffRoleIDs:     staffRoles,
		DatabaseURL:      dbURL,
		LogLevel:         os.Getenv("LOG_LEVEL"),
		AssetsDir:        assetsDir,
		LogFilePath:      logFilePath,
		LogMaxSizeMB:     logMaxSize,
		LogMaxBackups:    logMaxBackups,
		LogMaxAgeDays:    logMaxAge,
		LogFormat:        logFormat,
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate ensures the bot will not start in an insecure or unconfigured state.
func (c *Config) Validate() error {
	if c.DiscordToken == "" {
		return errors.New("DISCORD_TOKEN is required")
	}
	if c.GuildID == "" {
		return errors.New("DISCORD_GUILD_ID is required")
	}
	if c.VerifiedRoleID == "" {
		return errors.New("DISCORD_VERIFIED_ROLE_ID is required")
	}
	if c.UnverifiedRoleID == "" {
		return errors.New("DISCORD_UNVERIFIED_ROLE_ID is required")
	}
	if len(c.StaffRoleIDs) == 0 {
		return errors.New("ALLOWED_VERIFIER_ROLE_IDS must contain at least one Staff or Server Helper role ID")
	}
	if c.DatabaseURL == "" {
		return errors.New("database configuration (DATABASE_URL or DB_* vars) is required")
	}
	return nil
}

func parseIntEnv(key string, fallback int) int {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}