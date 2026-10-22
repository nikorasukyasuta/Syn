package handlers

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5/pgxpool"

	"syn/internal/discord/queue"
	"syn/internal/discord/syncutil"
	"syn/internal/version"
)

// HealthHandler manages the /health command, delivering public status for all members
// and detailed infrastructure diagnostics (Docker, EC2, DB, Queue) exclusively to Staff.
type HealthHandler struct {
	staffRoleMap map[string]struct{}
	dbPool       *pgxpool.Pool
	cmdQueue     *queue.CommandQueue
	lockMgr      *syncutil.UserLockManager
	startTime    time.Time
}

// NewHealthHandler constructs a new HealthHandler.
func NewHealthHandler(
	staffRoleIDs []string,
	dbPool *pgxpool.Pool,
	cmdQueue *queue.CommandQueue,
	lockMgr *syncutil.UserLockManager,
) *HealthHandler {
	staffMap := make(map[string]struct{}, len(staffRoleIDs))
	for _, id := range staffRoleIDs {
		staffMap[id] = struct{}{}
	}

	return &HealthHandler{
		staffRoleMap: staffMap,
		dbPool:       dbPool,
		cmdQueue:     cmdQueue,
		lockMgr:      lockMgr,
		startTime:    time.Now().UTC(),
	}
}

// HealthCommandDefinition defines the /health command visible to all users.
func HealthCommandDefinition() *discordgo.ApplicationCommand {
	dmPermission := false

	return &discordgo.ApplicationCommand{
		Name:         "health",
		Description:  "Check system status and operational health of the bot",
		DMPermission: &dmPermission,
	}
}

// HandleHealthCommand routes public vs staff telemetry based on caller roles.
func (h *HealthHandler) HandleHealthCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	isStaff := h.isStaff(i)
	embed := h.buildHealthEmbed(s, isStaff)

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		})
	}
}

func (h *HealthHandler) isStaff(i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	for _, r := range i.Member.Roles {
		if _, ok := h.staffRoleMap[r]; ok {
			return true
		}
	}
	return false
}

func (h *HealthHandler) buildHealthEmbed(s *discordgo.Session, isStaff bool) *discordgo.MessageEmbed {
	uptime := time.Since(h.startTime).Round(time.Second)
	ping := s.HeartbeatLatency().Round(time.Millisecond)
	if ping < 0 {
		ping = 0
	}

	if !isStaff {
		// Tier 1: Public View (All Users)
		return &discordgo.MessageEmbed{
			Title:       "🟢 System Operational • Syn Bot Health",
			Description: "All core bot systems and Discord gateway connections are operating normally.",
			Color:       0x2ECC71, // Green
			Fields: []*discordgo.MessageEmbedField{
				{
					Name:   "Bot Status",
					Value:  "🟢 Online & Nominal",
					Inline: true,
				},
				{
					Name:   "Uptime",
					Value:  fmt.Sprintf("%s (since <t:%d:R>)", formatDuration(uptime), h.startTime.Unix()),
					Inline: true,
				},
				{
					Name:   "Gateway Ping",
					Value:  fmt.Sprintf("`%d ms`", ping.Milliseconds()),
					Inline: true,
				},
				{
					Name:   "Version",
					Value:  fmt.Sprintf("`%s`", version.CurrentVersion),
					Inline: true,
				},
			},
			Footer: &discordgo.MessageEmbedFooter{
				Text: "ℹ️ Detailed infrastructure telemetry is restricted to server staff.",
			},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
	}

	// Tier 2: Staff Extended Telemetry View
	// 1. Memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapAllocMB := float64(m.Alloc) / (1024 * 1024)
	sysMB := float64(m.Sys) / (1024 * 1024)

	// 2. Database stats
	dbStatus := "🔴 Disconnected"
	dbPingStr := "N/A"
	dbPoolStatsStr := "N/A"
	if h.dbPool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		dbStart := time.Now()
		if err := h.dbPool.Ping(ctx); err == nil {
			dbPing := time.Since(dbStart).Round(time.Millisecond)
			dbStatus = "🟢 Connected"
			dbPingStr = fmt.Sprintf("`%d ms`", dbPing.Milliseconds())
			pStat := h.dbPool.Stat()
			dbPoolStatsStr = fmt.Sprintf("Total: `%d` | Idle: `%d` | Active: `%d` (Max: `%d`)",
				pStat.TotalConns(), pStat.IdleConns(), pStat.AcquiredConns(), pStat.MaxConns())
		}
		cancel()
	}

	// 3. Queue stats
	queueStatsStr := "N/A"
	if h.cmdQueue != nil {
		qStat := h.cmdQueue.Stats()
		queueStatsStr = fmt.Sprintf("Depth: `%d/%d` | Workers: `%d` | Processed: `%d` | Dropped: `%d`",
			qStat.QueueDepth, qStat.QueueCapacity, qStat.WorkersCount, qStat.TotalProcessed, qStat.TotalDropped)
	}

	// 4. User locks
	activeLocks := 0
	if h.lockMgr != nil {
		activeLocks = h.lockMgr.ActiveLocksCount()
	}

	// 5. Host & EC2 Info
	containerID, _ := os.Hostname()
	loadAvg := readLoadAverage()
	hostMem := readHostMemory()

	return &discordgo.MessageEmbed{
		Title:       "🛠️ Infrastructure & System Diagnostics (Staff Only)",
		Description: "Real-time health telemetry across Bot processes, Docker runtime, PostgreSQL database, and EC2 host instance.",
		Color:       0x3498DB, // Blue
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🤖 Bot Gateway & Process",
				Value:  fmt.Sprintf("Status: 🟢 **Healthy**\nUptime: `%s`\nGateway Ping: `%d ms`\nGoroutines: `%d`\nActive User Locks: `%d`", formatDuration(uptime), ping.Milliseconds(), runtime.NumGoroutine(), activeLocks),
				Inline: false,
			},
			{
				Name:   "🐘 PostgreSQL Database",
				Value:  fmt.Sprintf("Status: %s\nPing: %s\nConnection Pool: %s", dbStatus, dbPingStr, dbPoolStatsStr),
				Inline: false,
			},
			{
				Name:   "📬 Command Worker Queue",
				Value:  queueStatsStr,
				Inline: false,
			},
			{
				Name:   "🐳 Docker Container Runtime",
				Value:  fmt.Sprintf("Container ID: `%s`\nGo Runtime: `%s`\nArchitecture: `%s/%s`\nMemory: Heap `%.2f MB` / Sys `%.2f MB` (GCs: `%d`)", containerID, runtime.Version(), runtime.GOOS, runtime.GOARCH, heapAllocMB, sysMB, m.NumGC),
				Inline: false,
			},
			{
				Name:   "☁️ EC2 Host Instance (2 Cores Optimized)",
				Value:  fmt.Sprintf("Logical CPUs: `%d` (GOMAXPROCS: `%d`)\nCPU Load Avg: `%s`\nHost Memory: `%s`", runtime.NumCPU(), runtime.GOMAXPROCS(0), loadAvg, hostMem),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Syn Production Suite • Version %s", version.CurrentVersion),
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func readLoadAverage() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "N/A"
	}
	parts := strings.Fields(string(data))
	if len(parts) >= 3 {
		return fmt.Sprintf("%s, %s, %s (1m, 5m, 15m)", parts[0], parts[1], parts[2])
	}
	return strings.TrimSpace(string(data))
}

func readHostMemory() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "N/A"
	}

	var totalKB, availKB float64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			totalKB = parseMemKB(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			availKB = parseMemKB(line)
		}
	}

	if totalKB > 0 && availKB > 0 {
		usedMB := (totalKB - availKB) / 1024
		totalMB := totalKB / 1024
		return fmt.Sprintf("%.0f MB / %.0f MB (%.1f%% Used)", usedMB, totalMB, (usedMB/totalMB)*100)
	}
	return "N/A"
}

func parseMemKB(line string) float64 {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		val, _ := strconv.ParseFloat(fields[1], 64)
		return val
	}
	return 0
}