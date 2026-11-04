package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"syn/internal/domain"
)

// HoneypotDiscord is the subset of the Discord REST API used by HoneypotService.
// *discordgo.Session satisfies this interface; tests can supply a fake.
type HoneypotDiscord interface {
	GuildMember(guildID, userID string, options ...discordgo.RequestOption) (*discordgo.Member, error)
	GuildRoles(guildID string, options ...discordgo.RequestOption) ([]*discordgo.Role, error)
	GuildMemberEdit(guildID, userID string, data *discordgo.GuildMemberParams, options ...discordgo.RequestOption) (*discordgo.Member, error)
	GuildMemberRoleAdd(guildID, userID, roleID string, options ...discordgo.RequestOption) error
	GuildMemberRoleRemove(guildID, userID, roleID string, options ...discordgo.RequestOption) error
	GuildMemberDeleteWithReason(guildID, userID, reason string, options ...discordgo.RequestOption) error
}

// Quarantine outcome labels passed to the outcome hook.
const (
	OutcomeKicked         = "kicked"
	OutcomeLeft           = "left"
	OutcomeManualRoleDrop = "spammer_role_removed"
)

// QuarantineOutcomeHook is invoked when a quarantine is resolved automatically (kick, member left,
// or Spammer role removed manually by staff) so the handler can notify staff.
type QuarantineOutcomeHook func(q *domain.SpamQuarantine, outcome string)

// HoneypotService manages the honeypot spam trap configuration, spammer quarantines,
// role stripping/restoration, and the automatic 3-day kick deadline.
type HoneypotService struct {
	repo      domain.HoneypotRepository
	audit     domain.AuditRepository
	logger    *slog.Logger
	kickDelay time.Duration
	now       func() time.Time

	mu               sync.Mutex
	discord          HoneypotDiscord
	guildID          string
	channelID        string
	spammerRoleID    string
	warningMessageID string
	active           map[string]*domain.SpamQuarantine // userID -> active quarantine
	timers           map[string]*time.Timer            // userID -> kick timer
	inFlight         map[string]struct{}               // userID -> quarantine/kick in progress
	outcomeHook      QuarantineOutcomeHook
}

// NewHoneypotService constructs a HoneypotService with the default 3-day kick delay.
func NewHoneypotService(repo domain.HoneypotRepository, audit domain.AuditRepository, logger *slog.Logger) *HoneypotService {
	if logger == nil {
		logger = slog.Default()
	}
	return &HoneypotService{
		repo:      repo,
		audit:     audit,
		logger:    logger,
		kickDelay: domain.DefaultQuarantineKickDelay,
		now:       time.Now,
		active:    make(map[string]*domain.SpamQuarantine),
		timers:    make(map[string]*time.Timer),
		inFlight:  make(map[string]struct{}),
	}
}

// SetKickDelay overrides the auto-kick delay (used by tests).
func (s *HoneypotService) SetKickDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d > 0 {
		s.kickDelay = d
	}
}

// KickDelay returns the configured auto-kick delay.
func (s *HoneypotService) KickDelay() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kickDelay
}

// SetDiscord attaches the Discord API client (normally the bot session).
func (s *HoneypotService) SetDiscord(d HoneypotDiscord) {
	s.mu.Lock()
	s.discord = d
	s.mu.Unlock()
}

// SetOutcomeHook registers a callback for automatic quarantine resolutions.
func (s *HoneypotService) SetOutcomeHook(hook QuarantineOutcomeHook) {
	s.mu.Lock()
	s.outcomeHook = hook
	s.mu.Unlock()
}

// Init loads the honeypot configuration and all active quarantines, resumes kick timers
// (kicking immediately anyone whose deadline passed while the bot was offline), and starts
// a background sweeper. Non-empty cfgChannelID / cfgRoleID (from discord_ids.json) take precedence.
func (s *HoneypotService) Init(ctx context.Context, d HoneypotDiscord, guildID, cfgChannelID, cfgRoleID string) error {
	s.mu.Lock()
	s.discord = d
	s.guildID = guildID
	s.mu.Unlock()

	var dbCfg *domain.HoneypotConfig
	if s.repo != nil {
		loaded, err := s.repo.GetConfig(ctx, guildID)
		if err != nil {
			s.logger.Warn("failed to load honeypot config from database", "error", err)
		}
		dbCfg = loaded
	}

	s.mu.Lock()
	if dbCfg != nil {
		s.channelID = dbCfg.ChannelID
		s.spammerRoleID = dbCfg.SpammerRoleID
		s.warningMessageID = dbCfg.WarningMessageID
	}
	if v := strings.TrimSpace(cfgChannelID); v != "" {
		s.channelID = v
	}
	if v := strings.TrimSpace(cfgRoleID); v != "" {
		s.spammerRoleID = v
	}
	channelID, roleID := s.channelID, s.spammerRoleID
	s.mu.Unlock()

	s.logger.Info("honeypot configuration loaded", "channel_id", channelID, "spammer_role_id", roleID)

	if s.repo != nil {
		list, err := s.repo.ListActiveQuarantines(ctx, guildID)
		if err != nil {
			s.logger.Warn("failed to load active spam quarantines", "error", err)
		} else {
			for _, q := range list {
				s.mu.Lock()
				s.active[q.UserID] = q
				s.mu.Unlock()
				s.scheduleKick(q)
			}
			s.logger.Info("resumed active spam quarantines", "count", len(list))
		}
	}

	go s.runSweeper(ctx)
	return nil
}

// Config returns a snapshot of the current honeypot configuration.
func (s *HoneypotService) Config() domain.HoneypotConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return domain.HoneypotConfig{
		GuildID:          s.guildID,
		ChannelID:        s.channelID,
		SpammerRoleID:    s.spammerRoleID,
		WarningMessageID: s.warningMessageID,
	}
}

// SetConfig updates and persists the honeypot channel, Spammer role, and pinned warning message.
func (s *HoneypotService) SetConfig(ctx context.Context, guildID, channelID, roleID, warningMessageID string) error {
	s.mu.Lock()
	if guildID != "" {
		s.guildID = guildID
	}
	s.channelID = channelID
	s.spammerRoleID = roleID
	s.warningMessageID = warningMessageID
	cfg := &domain.HoneypotConfig{
		GuildID:          s.guildID,
		ChannelID:        channelID,
		SpammerRoleID:    roleID,
		WarningMessageID: warningMessageID,
	}
	s.mu.Unlock()

	if s.repo != nil {
		return s.repo.SaveConfig(ctx, cfg)
	}
	return nil
}

// IsHoneypotChannel reports whether channelID is the configured honeypot channel.
func (s *HoneypotService) IsHoneypotChannel(channelID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return channelID != "" && channelID == s.channelID
}

// GetActive returns the user's active quarantine, if any.
func (s *HoneypotService) GetActive(userID string) *domain.SpamQuarantine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[userID]
}

// ActiveQuarantines returns a snapshot of all active quarantines sorted by kick deadline.
func (s *HoneypotService) ActiveQuarantines() []*domain.SpamQuarantine {
	s.mu.Lock()
	list := make([]*domain.SpamQuarantine, 0, len(s.active))
	for _, q := range s.active {
		list = append(list, q)
	}
	s.mu.Unlock()
	sort.Slice(list, func(a, b int) bool { return list[a].KickAt.Before(list[b].KickAt) })
	return list
}

// Quarantine strips the member's removable roles, assigns the Spammer role, persists the quarantine,
// and schedules the automatic kick. Returns (quarantine, created, error); created is false if the
// user was already quarantined or a quarantine for them is already in progress.
func (s *HoneypotService) Quarantine(ctx context.Context, guildID, userID, username, channelID, messageID string) (*domain.SpamQuarantine, bool, error) {
	s.mu.Lock()
	roleID, d, delay := s.spammerRoleID, s.discord, s.kickDelay
	if roleID == "" || d == nil {
		s.mu.Unlock()
		return nil, false, domain.ErrHoneypotNotConfigured
	}
	if existing := s.active[userID]; existing != nil {
		s.mu.Unlock()
		return existing, false, nil
	}
	if _, busy := s.inFlight[userID]; busy {
		s.mu.Unlock()
		return nil, false, nil
	}
	s.inFlight[userID] = struct{}{}
	s.mu.Unlock()
	defer s.clearInFlight(userID)

	removed, err := s.applyQuarantineRoles(d, guildID, userID, roleID)
	if err != nil {
		return nil, false, err
	}

	now := s.now().UTC()
	q := &domain.SpamQuarantine{
		GuildID:          guildID,
		UserID:           userID,
		Username:         truncate(username, 100),
		ChannelID:        channelID,
		TriggerMessageID: messageID,
		RemovedRoleIDs:   removed,
		Status:           domain.QuarantineActive,
		QuarantinedAt:    now,
		KickAt:           now.Add(delay),
	}

	if s.repo != nil {
		if err := s.repo.CreateQuarantine(ctx, q); err != nil {
			s.logger.Error("failed to persist spam quarantine; continuing with in-memory quarantine",
				"user_id", userID, "error", err)
		}
	}

	s.mu.Lock()
	s.active[userID] = q
	s.mu.Unlock()
	s.scheduleKick(q)
	s.logAudit(ctx, "system:honeypot", userID, "honeypot_quarantine")

	s.logger.Warn("member quarantined by honeypot",
		"guild_id", guildID,
		"user_id", userID,
		"username", username,
		"removed_roles", len(removed),
		"kick_at", q.KickAt.Format(time.RFC3339),
	)
	return q, true, nil
}

// ReapplyQuarantine re-assigns the Spammer role (and strips join roles) when a quarantined
// member leaves and rejoins to evade the quarantine. The original removed-role list is preserved.
func (s *HoneypotService) ReapplyQuarantine(guildID, userID string) error {
	s.mu.Lock()
	q, roleID, d := s.active[userID], s.spammerRoleID, s.discord
	s.mu.Unlock()
	if q == nil {
		return domain.ErrNotQuarantined
	}
	if roleID == "" || d == nil {
		return domain.ErrHoneypotNotConfigured
	}
	_, err := s.applyQuarantineRoles(d, guildID, userID, roleID)
	if err == nil {
		s.logger.Warn("re-applied spam quarantine to member who rejoined", "user_id", userID)
	}
	return err
}

// Release lifts a quarantine: restores the member's previously stripped roles, removes the Spammer
// role, cancels the kick timer, and marks the record released. If the returned quarantine is non-nil
// the release was recorded; a non-nil error alongside it indicates a partial role restoration.
func (s *HoneypotService) Release(ctx context.Context, guildID, userID, actorID string) (*domain.SpamQuarantine, error) {
	s.mu.Lock()
	q := s.active[userID]
	delete(s.active, userID)
	if t := s.timers[userID]; t != nil {
		t.Stop()
		delete(s.timers, userID)
	}
	roleID, d := s.spammerRoleID, s.discord
	s.mu.Unlock()

	if q == nil && s.repo != nil {
		if dbQ, err := s.repo.GetActiveQuarantine(ctx, guildID, userID); err == nil {
			q = dbQ
		}
	}

	if q == nil {
		// Courtesy cleanup: strip a manually-assigned Spammer role even without a quarantine record.
		if roleID != "" && d != nil {
			_ = d.GuildMemberRoleRemove(guildID, userID, roleID)
		}
		return nil, domain.ErrNotQuarantined
	}

	var restoreErr error
	if d != nil {
		restoreErr = s.restoreRoles(d, guildID, userID, roleID, q.RemovedRoleIDs)
	}

	s.resolve(ctx, q, domain.QuarantineReleased, actorID)
	s.logAudit(ctx, actorID, userID, "honeypot_release")
	s.logger.Info("spam quarantine released", "user_id", userID, "actor_id", actorID, "restore_error", restoreErr)
	return q, restoreErr
}

func (s *HoneypotService) clearInFlight(userID string) {
	s.mu.Lock()
	delete(s.inFlight, userID)
	s.mu.Unlock()
}

// applyQuarantineRoles replaces the member's roles with {Spammer} plus any managed (integration/booster)
// roles that cannot be removed. Returns the role IDs that were stripped so they can be restored later.
func (s *HoneypotService) applyQuarantineRoles(d HoneypotDiscord, guildID, userID, spammerRoleID string) ([]string, error) {
	member, err := d.GuildMember(guildID, userID)
	if err != nil {
		return nil, fmt.Errorf("fetching member for quarantine: %w", err)
	}

	managed := map[string]bool{}
	if roles, err := d.GuildRoles(guildID); err == nil {
		for _, r := range roles {
			if r != nil && r.Managed {
				managed[r.ID] = true
			}
		}
	}

	keep := []string{spammerRoleID}
	removed := []string{}
	for _, r := range member.Roles {
		switch {
		case r == spammerRoleID || r == guildID:
			continue
		case managed[r]:
			keep = append(keep, r)
		default:
			removed = append(removed, r)
		}
	}

	if _, err := d.GuildMemberEdit(guildID, userID, &discordgo.GuildMemberParams{Roles: &keep}); err == nil {
		return removed, nil
	} else {
		s.logger.Warn("bulk quarantine role edit failed; falling back to per-role updates",
			"user_id", userID, "error", err)
	}

	if err := d.GuildMemberRoleAdd(guildID, userID, spammerRoleID); err != nil {
		return nil, fmt.Errorf("assigning spammer role (check the bot's role is ABOVE the Spammer role and has Manage Roles): %w", err)
	}
	actuallyRemoved := make([]string, 0, len(removed))
	for _, r := range removed {
		if err := d.GuildMemberRoleRemove(guildID, userID, r); err != nil {
			s.logger.Warn("could not strip role during quarantine (role likely above the bot)",
				"user_id", userID, "role_id", r, "error", err)
			continue
		}
		actuallyRemoved = append(actuallyRemoved, r)
	}
	return actuallyRemoved, nil
}

// restoreRoles removes the Spammer role and re-adds previously stripped roles that still exist.
func (s *HoneypotService) restoreRoles(d HoneypotDiscord, guildID, userID, spammerRoleID string, removed []string) error {
	member, err := d.GuildMember(guildID, userID)
	if err != nil {
		if isUnknownMember(err) {
			return nil // member already left; nothing to restore
		}
		return fmt.Errorf("fetching member for release: %w", err)
	}

	existing := map[string]bool{}
	if roles, err := d.GuildRoles(guildID); err == nil {
		for _, r := range roles {
			if r != nil {
				existing[r.ID] = true
			}
		}
	}

	seen := map[string]bool{}
	next := make([]string, 0, len(member.Roles)+len(removed))
	add := func(id string) {
		if id == "" || id == spammerRoleID || id == guildID || seen[id] {
			return
		}
		if len(existing) > 0 && !existing[id] {
			return // role was deleted from the server since quarantine
		}
		seen[id] = true
		next = append(next, id)
	}
	for _, r := range member.Roles {
		add(r)
	}
	for _, r := range removed {
		add(r)
	}

	if _, err := d.GuildMemberEdit(guildID, userID, &discordgo.GuildMemberParams{Roles: &next}); err == nil {
		return nil
	}

	// Fallback: remove Spammer role, then re-add each role individually.
	var errs []error
	if spammerRoleID != "" {
		if err := d.GuildMemberRoleRemove(guildID, userID, spammerRoleID); err != nil {
			errs = append(errs, fmt.Errorf("removing spammer role: %w", err))
		}
	}
	for _, r := range removed {
		if !seen[r] {
			continue
		}
		if err := d.GuildMemberRoleAdd(guildID, userID, r); err != nil {
			errs = append(errs, fmt.Errorf("restoring role %s: %w", r, err))
		}
	}
	return errors.Join(errs...)
}

func (s *HoneypotService) scheduleKick(q *domain.SpamQuarantine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	minWait := 5 * time.Second
	if s.kickDelay < minWait {
		minWait = 10 * time.Millisecond
	}
	wait := q.KickAt.Sub(s.now())
	if wait < minWait {
		wait = minWait
	}
	if t := s.timers[q.UserID]; t != nil {
		t.Stop()
	}
	userID := q.UserID
	s.timers[userID] = time.AfterFunc(wait, func() { s.enforceKick(userID) })
}

func (s *HoneypotService) rescheduleIn(userID string, wait time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.active[userID]; !ok {
		return
	}
	if t := s.timers[userID]; t != nil {
		t.Stop()
	}
	s.timers[userID] = time.AfterFunc(wait, func() { s.enforceKick(userID) })
}

// enforceKick kicks a quarantined member whose 3-day deadline has elapsed, unless staff
// already removed their Spammer role manually or they left the server.
func (s *HoneypotService) enforceKick(userID string) {
	s.mu.Lock()
	q, roleID, d, hook := s.active[userID], s.spammerRoleID, s.discord, s.outcomeHook
	if q == nil || d == nil {
		s.mu.Unlock()
		return
	}
	if _, busy := s.inFlight[userID]; busy {
		s.mu.Unlock()
		return
	}
	s.inFlight[userID] = struct{}{}
	s.mu.Unlock()
	defer s.clearInFlight(userID)

	if s.now().Before(q.KickAt) {
		s.scheduleKick(q)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	member, err := d.GuildMember(q.GuildID, userID)
	if err != nil {
		if isUnknownMember(err) {
			s.forget(userID)
			s.resolve(ctx, q, domain.QuarantineLeft, "system:member-left")
			if hook != nil {
				hook(q, OutcomeLeft)
			}
			return
		}
		s.logger.Warn("could not fetch quarantined member before kick; retrying in 10 minutes", "user_id", userID, "error", err)
		s.rescheduleIn(userID, 10*time.Minute)
		return
	}

	if roleID != "" && !containsString(member.Roles, roleID) {
		// Staff removed the Spammer role manually: treat as a release and restore stripped roles.
		s.forget(userID)
		if rErr := s.restoreRoles(d, q.GuildID, userID, roleID, q.RemovedRoleIDs); rErr != nil {
			s.logger.Warn("partial role restore after manual spammer role removal", "user_id", userID, "error", rErr)
		}
		s.resolve(ctx, q, domain.QuarantineReleased, "system:spammer-role-removed")
		if hook != nil {
			hook(q, OutcomeManualRoleDrop)
		}
		return
	}

	reason := "Syn honeypot: posted in the spam trap channel and was not released by staff within 3 days"
	if err := d.GuildMemberDeleteWithReason(q.GuildID, userID, reason); err != nil {
		if isUnknownMember(err) {
			s.forget(userID)
			s.resolve(ctx, q, domain.QuarantineLeft, "system:member-left")
			if hook != nil {
				hook(q, OutcomeLeft)
			}
			return
		}
		s.logger.Error("failed to auto-kick quarantined spammer (bot needs Kick Members and a role above the member); retrying in 30 minutes",
			"user_id", userID, "error", err)
		s.rescheduleIn(userID, 30*time.Minute)
		return
	}

	s.forget(userID)
	s.resolve(ctx, q, domain.QuarantineKicked, "system:auto-kick")
	s.logAudit(ctx, "system:honeypot", userID, "honeypot_auto_kick")
	s.logger.Warn("auto-kicked quarantined spammer after deadline", "user_id", userID, "username", q.Username)
	if hook != nil {
		hook(q, OutcomeKicked)
	}
}

// forget removes a user from the in-memory active set and cancels their timer.
func (s *HoneypotService) forget(userID string) {
	s.mu.Lock()
	delete(s.active, userID)
	if t := s.timers[userID]; t != nil {
		t.Stop()
		delete(s.timers, userID)
	}
	s.mu.Unlock()
}

func (s *HoneypotService) resolve(ctx context.Context, q *domain.SpamQuarantine, status domain.QuarantineStatus, actor string) {
	now := s.now().UTC()
	q.Status = status
	q.ResolvedAt = &now
	q.ResolvedBy = actor
	if s.repo != nil {
		if err := s.repo.ResolveQuarantine(ctx, q.GuildID, q.UserID, status, actor); err != nil {
			s.logger.Error("failed to persist quarantine resolution", "user_id", q.UserID, "status", status, "error", err)
		}
	}
}

// runSweeper periodically enforces any overdue kicks as a safety net for missed timers.
func (s *HoneypotService) runSweeper(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := s.now()
			for _, q := range s.ActiveQuarantines() {
				if !now.Before(q.KickAt) {
					go s.enforceKick(q.UserID)
				}
			}
		}
	}
}

func (s *HoneypotService) logAudit(ctx context.Context, actorID, targetID, action string) {
	if s.audit == nil {
		return
	}
	auditCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.audit.LogAudit(auditCtx, actorID, targetID, action); err != nil {
		s.logger.Warn("failed to write honeypot audit log", "action", action, "error", err)
	}
}

// isUnknownMember reports whether a Discord REST error means the member is no longer in the guild.
func isUnknownMember(err error) bool {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) {
		if restErr.Message != nil && (restErr.Message.Code == discordgo.ErrCodeUnknownMember || restErr.Message.Code == discordgo.ErrCodeUnknownUser) {
			return true
		}
		if restErr.Response != nil && restErr.Response.StatusCode == http.StatusNotFound {
			return true
		}
	}
	return false
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

