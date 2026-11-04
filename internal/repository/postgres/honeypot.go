package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"syn/internal/domain"
)

// HoneypotRepo implements domain.HoneypotRepository backed by PostgreSQL.
type HoneypotRepo struct {
	pool *pgxpool.Pool
}

// NewHoneypotRepo constructs a new HoneypotRepo.
func NewHoneypotRepo(pool *pgxpool.Pool) *HoneypotRepo {
	return &HoneypotRepo{pool: pool}
}

// SaveConfig upserts the honeypot channel / Spammer role configuration for a guild.
func (r *HoneypotRepo) SaveConfig(ctx context.Context, cfg *domain.HoneypotConfig) error {
	const query = `
		INSERT INTO honeypot_config (guild_id, channel_id, spammer_role_id, warning_message_id, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (guild_id) DO UPDATE SET
			channel_id         = EXCLUDED.channel_id,
			spammer_role_id    = EXCLUDED.spammer_role_id,
			warning_message_id = EXCLUDED.warning_message_id,
			updated_at         = NOW()
	`
	if _, err := r.pool.Exec(ctx, query, cfg.GuildID, cfg.ChannelID, cfg.SpammerRoleID, cfg.WarningMessageID); err != nil {
		return fmt.Errorf("saving honeypot config: %w", err)
	}
	return nil
}

// GetConfig returns the honeypot configuration for a guild, or nil if none exists.
func (r *HoneypotRepo) GetConfig(ctx context.Context, guildID string) (*domain.HoneypotConfig, error) {
	const query = `
		SELECT guild_id, channel_id, spammer_role_id, warning_message_id, updated_at
		FROM honeypot_config WHERE guild_id = $1
	`
	cfg := &domain.HoneypotConfig{}
	err := r.pool.QueryRow(ctx, query, guildID).Scan(
		&cfg.GuildID, &cfg.ChannelID, &cfg.SpammerRoleID, &cfg.WarningMessageID, &cfg.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying honeypot config: %w", err)
	}
	return cfg, nil
}

// CreateQuarantine inserts a new active quarantine. If the user already has an active
// quarantine in this guild, the insert is a no-op.
func (r *HoneypotRepo) CreateQuarantine(ctx context.Context, q *domain.SpamQuarantine) error {
	const query = `
		INSERT INTO spam_quarantines (
			guild_id, user_id, username, channel_id, trigger_message_id,
			removed_role_ids, status, quarantined_at, kick_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $8)
		ON CONFLICT (guild_id, user_id) WHERE status = 'active' DO NOTHING
		RETURNING id::text
	`
	roles := q.RemovedRoleIDs
	if roles == nil {
		roles = []string{}
	}
	err := r.pool.QueryRow(ctx, query,
		q.GuildID, q.UserID, q.Username, q.ChannelID, q.TriggerMessageID,
		roles, q.QuarantinedAt, q.KickAt,
	).Scan(&q.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("creating spam quarantine: %w", err)
	}
	return nil
}

const quarantineColumns = `
	id::text, guild_id, user_id, username, channel_id, trigger_message_id,
	removed_role_ids, status, quarantined_at, kick_at, resolved_at, resolved_by
`

func scanQuarantine(row pgx.Row) (*domain.SpamQuarantine, error) {
	q := &domain.SpamQuarantine{}
	var status string
	if err := row.Scan(
		&q.ID, &q.GuildID, &q.UserID, &q.Username, &q.ChannelID, &q.TriggerMessageID,
		&q.RemovedRoleIDs, &status, &q.QuarantinedAt, &q.KickAt, &q.ResolvedAt, &q.ResolvedBy,
	); err != nil {
		return nil, err
	}
	q.Status = domain.QuarantineStatus(status)
	return q, nil
}

// GetActiveQuarantine returns the user's active quarantine, or nil if none exists.
func (r *HoneypotRepo) GetActiveQuarantine(ctx context.Context, guildID, userID string) (*domain.SpamQuarantine, error) {
	query := `SELECT ` + quarantineColumns + ` FROM spam_quarantines
		WHERE guild_id = $1 AND user_id = $2 AND status = 'active'`
	q, err := scanQuarantine(r.pool.QueryRow(ctx, query, guildID, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying active spam quarantine: %w", err)
	}
	return q, nil
}

// ListActiveQuarantines returns all active quarantines for a guild ordered by kick deadline.
func (r *HoneypotRepo) ListActiveQuarantines(ctx context.Context, guildID string) ([]*domain.SpamQuarantine, error) {
	query := `SELECT ` + quarantineColumns + ` FROM spam_quarantines
		WHERE guild_id = $1 AND status = 'active' ORDER BY kick_at ASC`
	rows, err := r.pool.Query(ctx, query, guildID)
	if err != nil {
		return nil, fmt.Errorf("querying active spam quarantines: %w", err)
	}
	defer rows.Close()

	var list []*domain.SpamQuarantine
	for rows.Next() {
		q, err := scanQuarantine(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning spam quarantine: %w", err)
		}
		list = append(list, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating spam quarantines: %w", err)
	}
	return list, nil
}

// ResolveQuarantine marks the user's active quarantine as released, kicked, or left.
func (r *HoneypotRepo) ResolveQuarantine(ctx context.Context, guildID, userID string, status domain.QuarantineStatus, resolvedBy string) error {
	const query = `
		UPDATE spam_quarantines
		SET status = $3, resolved_at = NOW(), resolved_by = $4
		WHERE guild_id = $1 AND user_id = $2 AND status = 'active'
	`
	if _, err := r.pool.Exec(ctx, query, guildID, userID, string(status), resolvedBy); err != nil {
		return fmt.Errorf("resolving spam quarantine: %w", err)
	}
	return nil
}

