package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds enterprise application settings, database credentials, RBAC role mappings, and logging parameters.
type Config struct {
	DiscordToken        string
	GuildID             string
	VerifiedRoleID      string
	UnverifiedRoleID    string
	StaffRoleIDs        []string // Holds both Staff and Server Helper Role IDs
	StaffAlertChannelID string   // Optional: Channel to send staff notifications/safeword alerts
	SafewordEmoji       string   // Optional: Reaction emoji that triggers RED (defaults to "🛑")
	DatabaseURL         string   // Full PostgreSQL connection string
	AssetsDir           string
	LogLevel            string
	LogFilePath         string
	LogMaxSizeMB        int
	LogMaxBackups       int
	LogMaxAgeDays       int
	LogFormat           string
	TicketCategoryID    string // Optional: Category ID where ticket channels are created
	TicketLogChannelID  string // Optional: Channel ID where ticket closures are logged
	IDsFilePath         string // Path to the file where Discord IDs were loaded from (if applicable)
}

// DiscordIDs represents Discord snowflake identifiers loaded from a dedicated JSON configuration file.
type DiscordIDs struct {
	GuildID             string   `json:"guild_id"`
	VerifiedRoleID      string   `json:"verified_role_id"`
	UnverifiedRoleID    string   `json:"unverified_role_id"`
	StaffRoleIDs        []string `json:"staff_role_ids"`
	TicketCategoryID    string   `json:"ticket_category_id"`
	TicketLogChannelID  string   `json:"ticket_log_channel_id"`
	StaffAlertChannelID string   `json:"staff_alert_channel_id"`

	// Supports nested structure: "roles": { ... }, "channels": { ... }
	Roles struct {
		VerifiedRoleID   string   `json:"verified_role_id"`
		UnverifiedRoleID string   `json:"unverified_role_id"`
		StaffRoleIDs     []string `json:"staff_role_ids"`
	} `json:"roles"`

	Channels struct {
		TicketCategoryID    string `json:"ticket_category_id"`
		TicketLogChannelID  string `json:"ticket_log_channel_id"`
		StaffAlertChannelID string `json:"staff_alert_channel_id"`
	} `json:"channels"`
}

// LoadDiscordIDsFile reads and parses a dedicated JSON configuration file containing server IDs.
func LoadDiscordIDsFile(filePath string) (*DiscordIDs, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var ids DiscordIDs
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, fmt.Errorf("parsing discord IDs json file %s: %w", filePath, err)
	}

	// Consolidate nested values into top-level fields if top-level was blank
	if ids.VerifiedRoleID == "" && ids.Roles.VerifiedRoleID != "" {
		ids.VerifiedRoleID = ids.Roles.VerifiedRoleID
	}
	if ids.UnverifiedRoleID == "" && ids.Roles.UnverifiedRoleID != "" {
		ids.UnverifiedRoleID = ids.Roles.UnverifiedRoleID
	}
	if len(ids.StaffRoleIDs) == 0 && len(ids.Roles.StaffRoleIDs) > 0 {
		ids.StaffRoleIDs = ids.Roles.StaffRoleIDs
	}
	if ids.TicketCategoryID == "" && ids.Channels.TicketCategoryID != "" {
		ids.TicketCategoryID = ids.Channels.TicketCategoryID
	}
	if ids.TicketLogChannelID == "" && ids.Channels.TicketLogChannelID != "" {
		ids.TicketLogChannelID = ids.Channels.TicketLogChannelID
	}
	if ids.StaffAlertChannelID == "" && ids.Channels.StaffAlertChannelID != "" {
		ids.StaffAlertChannelID = ids.Channels.StaffAlertChannelID
	}

	return &ids, nil
}

// Load parses configuration from external JSON files and environment variables.
func Load() (*Config, error) {
	// 1. Attempt to load Discord IDs from external configuration file
	idsPath := os.Getenv("DISCORD_IDS_FILE")
	if idsPath == "" {
		idsPath = os.Getenv("DISCORD_RESOURCES_FILE")
	}

	var fileIDs *DiscordIDs
	var resolvedIDsPath string

	candidates := []string{}
	if idsPath != "" {
		candidates = append(candidates, idsPath)
	} else {
		candidates = append(candidates, "config/discord_ids.json", "config/ids.json", "discord_ids.json", "ids.json")
	}

	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			parsed, err := LoadDiscordIDsFile(cand)
			if err == nil {
				fileIDs = parsed
				resolvedIDsPath = cand
				break
			}
		}
	}

	guildID := ""
	verifiedRoleID := ""
	unverifiedRoleID := ""
	var staffRoles []string
	ticketCat := ""
	ticketLog := ""
	staffAlertChan := ""

	if fileIDs != nil {
		guildID = fileIDs.GuildID
		verifiedRoleID = fileIDs.VerifiedRoleID
		unverifiedRoleID = fileIDs.UnverifiedRoleID
		staffRoles = fileIDs.StaffRoleIDs
		ticketCat = fileIDs.TicketCategoryID
		ticketLog = fileIDs.TicketLogChannelID
		staffAlertChan = fileIDs.StaffAlertChannelID
	}

	// 2. Fallback to environment variables if not populated by external IDs file
	if guildID == "" {
		guildID = os.Getenv("DISCORD_GUILD_ID")
	}
	if verifiedRoleID == "" {
		verifiedRoleID = os.Getenv("DISCORD_VERIFIED_ROLE_ID")
	}
	if unverifiedRoleID == "" {
		unverifiedRoleID = os.Getenv("DISCORD_UNVERIFIED_ROLE_ID")
	}
	if len(staffRoles) == 0 {
		rawRoles := os.Getenv("ALLOWED_VERIFIER_ROLE_IDS")
		for _, r := range strings.Split(rawRoles, ",") {
			if trimmed := strings.TrimSpace(r); trimmed != "" {
				staffRoles = append(staffRoles, trimmed)
			}
		}
	}
	if ticketCat == "" {
		ticketCat = os.Getenv("DISCORD_TICKET_CATEGORY_ID")
		if ticketCat == "" {
			ticketCat = os.Getenv("TICKET_CATEGORY_ID")
		}
	}
	if ticketLog == "" {
		ticketLog = os.Getenv("DISCORD_TICKET_LOG_CHANNEL_ID")
		if ticketLog == "" {
			ticketLog = os.Getenv("TICKET_LOG_CHANNEL_ID")
		}
	}
	if staffAlertChan == "" {
		staffAlertChan = strings.TrimSpace(os.Getenv("STAFF_ALERT_CHANNEL_ID"))
	}

	safewordEmoji := strings.TrimSpace(os.Getenv("SAFEWORD_EMOJI"))
	if safewordEmoji == "" {
		safewordEmoji = "🛑"
	}

	// 3. Infrastructure & Secrets (Database, Logging, Tokens)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
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
		DiscordToken:        os.Getenv("DISCORD_TOKEN"),
		GuildID:             guildID,
		VerifiedRoleID:      verifiedRoleID,
		UnverifiedRoleID:    unverifiedRoleID,
		StaffRoleIDs:        staffRoles,
		StaffAlertChannelID: staffAlertChan,
		SafewordEmoji:       safewordEmoji,
		DatabaseURL:         dbURL,
		LogLevel:            os.Getenv("LOG_LEVEL"),
		AssetsDir:           assetsDir,
		LogFilePath:         logFilePath,
		LogMaxSizeMB:        logMaxSize,
		LogMaxBackups:       logMaxBackups,
		LogMaxAgeDays:       logMaxAge,
		LogFormat:           logFormat,
		TicketCategoryID:    ticketCat,
		TicketLogChannelID:  ticketLog,
		IDsFilePath:         resolvedIDsPath,
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate ensures the bot will not start in an insecure or unconfigured state.
func (c *Config) Validate() error {
	if c.DiscordToken == "" {
		return errors.New("DISCORD_TOKEN is required in .env")
	}
	if c.DatabaseURL == "" {
		return errors.New("database configuration (DATABASE_URL or DB_* vars) is required in .env")
	}
	if c.GuildID == "" {
		return errors.New("guild_id is required (specify in config/discord_ids.json or DISCORD_GUILD_ID)")
	}
	if c.VerifiedRoleID == "" {
		return errors.New("verified_role_id is required (specify in config/discord_ids.json or DISCORD_VERIFIED_ROLE_ID)")
	}
	if c.UnverifiedRoleID == "" {
		return errors.New("unverified_role_id is required (specify in config/discord_ids.json or DISCORD_UNVERIFIED_ROLE_ID)")
	}
	if len(c.StaffRoleIDs) == 0 {
		return errors.New("staff_role_ids must contain at least one Staff or Server Helper role ID (specify in config/discord_ids.json or ALLOWED_VERIFIER_ROLE_IDS)")
	}
	return nil
}

func parseIntEnv(key string, fallback int) int {
	valStr := os.Getenv(key)
	if valStr == "" {
		return fallback
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return fallback
	}
	return val
}
