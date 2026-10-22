package version

import (
	"fmt"
	"strings"
)

// Current version and source repository constants.
const (
	CurrentVersion   = "v1.2.0"
	GitRepositoryURL = "https://github.com/nikorasukyasuta/Syn"
	ReleaseDate      = "2026-10-22"
)

// Release represents a version milestone with date and feature highlights.
type Release struct {
	Version string
	Date    string
	Changes []string
}

// History contains the chronological release log for the Syn bot.
var History = []Release{
	{
		Version: "v1.2.0",
		Date:    "2026-10-22",
		Changes: []string{
			"Asynchronous Command Processing Queue with worker pool for zero dropped requests",
			"Fine-grained per-user Mutex locking for thread-safe member updates and role mutations",
			"Multi-core 2-CPU EC2 and Docker optimization",
			"Dual-tier Health Check (/health) with public status and staff telemetry",
			"Bot version tracking and GitHub source code repository link (/version)",
		},
	},
	{
		Version: "v1.1.0",
		Date:    "2026-10-22",
		Changes: []string{
			"Interactive Ticket Management System with 5 categories (Verification, Report, Bug, Account, Support)",
			"Automatic embedded PostgreSQL schema migrations on bot startup",
			"Decoupled Audit Logging repository and postgres implementation",
			"In-memory token-bucket command rate limiter & 3-second SLA anti-timeout protection",
			"Dedicated external discord_ids.json configuration file",
		},
	},
	{
		Version: "v1.0.0",
		Date:    "2026-10-21",
		Changes: []string{
			"18+ Member Age Verification with automatic role swaps (/verify-member)",
			"Reaction Role panels with local EC2 image asset attachments",
			"Emergency RED Safeword alert system with automatic 30s slowmode and 5m timers",
			"Real-time server resource cache for dynamic roles and channels",
		},
	},
}

// FormatSummary returns a concise formatted markdown string of the current release highlights.
func FormatSummary() string {
	if len(History) == 0 {
		return fmt.Sprintf("**Version:** `%s`", CurrentVersion)
	}
	latest := History[0]
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Current Version:** `%s` (%s)\n", latest.Version, latest.Date))
	b.WriteString(fmt.Sprintf("**Source Code:** [nikorasukyasuta/Syn](%s)\n\n", GitRepositoryURL))
	b.WriteString("**What's New in this Release:**\n")
	for _, change := range latest.Changes {
		b.WriteString(fmt.Sprintf("• %s\n", change))
	}
	return b.String()
}

