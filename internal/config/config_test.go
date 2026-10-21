package config_test

import (
	"os"
	"testing"

	"syn/internal/config"
)

func setEnv(t *testing.T, key, val string) {
	t.Helper()
	orig, exists := os.LookupEnv(key)
	if err := os.Setenv(key, val); err != nil {
		t.Fatalf("failed setting env %s: %v", key, err)
	}
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, orig)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		orig, exists := os.LookupEnv(k)
		_ = os.Unsetenv(k)
		kCopy := k
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(kCopy, orig)
			}
		})
	}
}

func TestConfig_Load_SuccessWithDatabaseURL(t *testing.T) {
	clearEnv(t, "DISCORD_TOKEN", "DISCORD_GUILD_ID", "DISCORD_VERIFIED_ROLE_ID", "DISCORD_UNVERIFIED_ROLE_ID", "ALLOWED_VERIFIER_ROLE_IDS", "DATABASE_URL", "ASSETS_DIR", "LOG_LEVEL", "LOG_FILE_PATH", "LOG_MAX_SIZE_MB", "LOG_MAX_BACKUPS", "LOG_MAX_AGE_DAYS", "LOG_FORMAT")

	setEnv(t, "DISCORD_TOKEN", "token_123")
	setEnv(t, "DISCORD_GUILD_ID", "guild_456")
	setEnv(t, "DISCORD_VERIFIED_ROLE_ID", "role_789")
	setEnv(t, "DISCORD_UNVERIFIED_ROLE_ID", "role_unverified_000")
	setEnv(t, "ALLOWED_VERIFIER_ROLE_IDS", "role_staff, role_helper")
	setEnv(t, "DATABASE_URL", "postgres://user:pass@localhost:5432/db?sslmode=disable")
	setEnv(t, "ASSETS_DIR", "/custom/assets")
	setEnv(t, "LOG_LEVEL", "DEBUG")
	setEnv(t, "LOG_FILE_PATH", "/var/log/syn.log")
	setEnv(t, "LOG_MAX_SIZE_MB", "20")
	setEnv(t, "LOG_MAX_BACKUPS", "10")
	setEnv(t, "LOG_MAX_AGE_DAYS", "60")
	setEnv(t, "LOG_FORMAT", "text")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DiscordToken != "token_123" {
		t.Errorf("expected token token_123, got %s", cfg.DiscordToken)
	}
	if cfg.GuildID != "guild_456" {
		t.Errorf("expected guild guild_456, got %s", cfg.GuildID)
	}
	if cfg.VerifiedRoleID != "role_789" {
		t.Errorf("expected verified role role_789, got %s", cfg.VerifiedRoleID)
	}
	if cfg.UnverifiedRoleID != "role_unverified_000" {
		t.Errorf("expected unverified role role_unverified_000, got %s", cfg.UnverifiedRoleID)
	}
	if len(cfg.StaffRoleIDs) != 2 || cfg.StaffRoleIDs[0] != "role_staff" || cfg.StaffRoleIDs[1] != "role_helper" {
		t.Errorf("unexpected staff roles: %v", cfg.StaffRoleIDs)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db?sslmode=disable" {
		t.Errorf("unexpected db url: %s", cfg.DatabaseURL)
	}
	if cfg.AssetsDir != "/custom/assets" {
		t.Errorf("unexpected assets dir: %s", cfg.AssetsDir)
	}
	if cfg.LogLevel != "DEBUG" {
		t.Errorf("unexpected log level: %s", cfg.LogLevel)
	}
	if cfg.LogFilePath != "/var/log/syn.log" {
		t.Errorf("unexpected log file path: %s", cfg.LogFilePath)
	}
	if cfg.LogMaxSizeMB != 20 || cfg.LogMaxBackups != 10 || cfg.LogMaxAgeDays != 60 || cfg.LogFormat != "text" {
		t.Errorf("unexpected log rotation settings: %+v", cfg)
	}
}

func TestConfig_Load_ConstructDatabaseURL(t *testing.T) {
	clearEnv(t, "DISCORD_TOKEN", "DISCORD_GUILD_ID", "DISCORD_VERIFIED_ROLE_ID", "DISCORD_UNVERIFIED_ROLE_ID", "ALLOWED_VERIFIER_ROLE_IDS", "DATABASE_URL", "DB_USER", "DB_PASSWORD", "DB_HOST", "DB_PORT", "DB_NAME", "DB_SSLMODE", "ASSETS_DIR")

	setEnv(t, "DISCORD_TOKEN", "tok")
	setEnv(t, "DISCORD_GUILD_ID", "gid")
	setEnv(t, "DISCORD_VERIFIED_ROLE_ID", "vrole")
	setEnv(t, "DISCORD_UNVERIFIED_ROLE_ID", "uvrole")
	setEnv(t, "ALLOWED_VERIFIER_ROLE_IDS", "staff")
	setEnv(t, "DB_USER", "admin")
	setEnv(t, "DB_PASSWORD", "secret")
	setEnv(t, "DB_HOST", "dbhost")
	setEnv(t, "DB_PORT", "5432")
	setEnv(t, "DB_NAME", "syn_db")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedURL := "postgres://admin:secret@dbhost:5432/syn_db?sslmode=disable"
	if cfg.DatabaseURL != expectedURL {
		t.Errorf("expected constructed URL %s, got %s", expectedURL, cfg.DatabaseURL)
	}
	if cfg.AssetsDir != "/app/assets/panels" {
		t.Errorf("expected default assets dir /app/assets/panels, got %s", cfg.AssetsDir)
	}
	if cfg.LogFilePath != "logs/syn.log" || cfg.LogMaxSizeMB != 10 || cfg.LogMaxBackups != 5 || cfg.LogMaxAgeDays != 30 {
		t.Errorf("expected default log settings, got %+v", cfg)
	}
}

func TestConfig_ValidationErrors(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.Config
		wantErrMsg string
	}{
		{
			name: "Missing Discord Token",
			cfg: config.Config{
				DiscordToken:     "",
				GuildID:          "g",
				VerifiedRoleID:   "v",
				UnverifiedRoleID: "uv",
				StaffRoleIDs:     []string{"s"},
				DatabaseURL:      "db",
			},
			wantErrMsg: "DISCORD_TOKEN is required",
		},
		{
			name: "Missing Guild ID",
			cfg: config.Config{
				DiscordToken:     "t",
				GuildID:          "",
				VerifiedRoleID:   "v",
				UnverifiedRoleID: "uv",
				StaffRoleIDs:     []string{"s"},
				DatabaseURL:      "db",
			},
			wantErrMsg: "DISCORD_GUILD_ID is required",
		},
		{
			name: "Missing Verified Role ID",
			cfg: config.Config{
				DiscordToken:     "t",
				GuildID:          "g",
				VerifiedRoleID:   "",
				UnverifiedRoleID: "uv",
				StaffRoleIDs:     []string{"s"},
				DatabaseURL:      "db",
			},
			wantErrMsg: "DISCORD_VERIFIED_ROLE_ID is required",
		},
		{
			name: "Missing Unverified Role ID",
			cfg: config.Config{
				DiscordToken:     "t",
				GuildID:          "g",
				VerifiedRoleID:   "v",
				UnverifiedRoleID: "",
				StaffRoleIDs:     []string{"s"},
				DatabaseURL:      "db",
			},
			wantErrMsg: "DISCORD_UNVERIFIED_ROLE_ID is required",
		},
		{
			name: "Missing Staff Roles",
			cfg: config.Config{
				DiscordToken:     "t",
				GuildID:          "g",
				VerifiedRoleID:   "v",
				UnverifiedRoleID: "uv",
				StaffRoleIDs:     []string{},
				DatabaseURL:      "db",
			},
			wantErrMsg: "ALLOWED_VERIFIER_ROLE_IDS must contain at least one Staff or Server Helper role ID",
		},
		{
			name: "Missing Database URL",
			cfg: config.Config{
				DiscordToken:     "t",
				GuildID:          "g",
				VerifiedRoleID:   "v",
				UnverifiedRoleID: "uv",
				StaffRoleIDs:     []string{"s"},
				DatabaseURL:      "",
			},
			wantErrMsg: "database configuration (DATABASE_URL or DB_* vars) is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrMsg)
			}
			if err.Error() != tc.wantErrMsg {
				t.Fatalf("expected error %q, got %q", tc.wantErrMsg, err.Error())
			}
		})
	}
}

