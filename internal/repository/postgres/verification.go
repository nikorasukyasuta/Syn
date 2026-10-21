package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"syn/internal/domain"
)

// VerificationRepo implements domain.VerificationRepository using pgxpool.
type VerificationRepo struct {
	pool *pgxpool.Pool
}

// NewVerificationRepo constructs a new PostgreSQL repository.
func NewVerificationRepo(pool *pgxpool.Pool) *VerificationRepo {
	return &VerificationRepo{pool: pool}
}

// GetByUserID retrieves a member's verification status using parameterized SQL.
func (r *VerificationRepo) GetByUserID(ctx context.Context, guildID, userID string) (*domain.Verification, error) {
	const query = `
		SELECT discord_user_id, guild_id, is_verified, verified_by_id, verified_at
		FROM member_verifications
		WHERE guild_id = $1 AND discord_user_id = $2
	`
	var v domain.Verification
	err := r.pool.QueryRow(ctx, query, guildID, userID).Scan(
		&v.DiscordUserID,
		&v.GuildID,
		&v.IsVerified,
		&v.VerifiedByID,
		&v.VerifiedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // Not found is not an application error
	}
	if err != nil {
		return nil, fmt.Errorf("querying verification record: %w", err)
	}
	return &v, nil
}

// SaveVerification persists the verification and writes an audit log atomically.
func (r *VerificationRepo) SaveVerification(ctx context.Context, v *domain.Verification) error {
	// pgx.BeginFunc automatically commits on nil error or rolls back on error/panic.
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		const upsertQuery = `
			INSERT INTO member_verifications (discord_user_id, guild_id, is_verified, verified_by_id, verified_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, NOW())
			ON CONFLICT (discord_user_id) DO UPDATE
			SET is_verified = EXCLUDED.is_verified,
			    verified_by_id = EXCLUDED.verified_by_id,
			    updated_at = NOW()
		`
		if _, err := tx.Exec(ctx, upsertQuery, v.DiscordUserID, v.GuildID, v.IsVerified, v.VerifiedByID, v.VerifiedAt); err != nil {
			return fmt.Errorf("upserting verification: %w", err)
		}

		const auditQuery = `
			INSERT INTO audit_logs (actor_id, target_id, action)
			VALUES ($1, $2, 'VERIFY_18_PLUS')
		`
		if _, err := tx.Exec(ctx, auditQuery, v.VerifiedByID, v.DiscordUserID); err != nil {
			return fmt.Errorf("inserting audit log: %w", err)
		}
		return nil
	})
}

// LogAudit records standalone security or moderation events.
func (r *VerificationRepo) LogAudit(ctx context.Context, actorID, targetID, action string) error {
	const query = `INSERT INTO audit_logs (actor_id, target_id, action) VALUES ($1, $2, $3)`
	_, err := r.pool.Exec(ctx, query, actorID, targetID, action)
	return err
}
