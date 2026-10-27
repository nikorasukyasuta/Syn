package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"syn/internal/domain"
)

// WarningRepo implements domain.WarningRepository using PostgreSQL.
type WarningRepo struct {
	pool *pgxpool.Pool
}

// NewWarningRepo constructs a new WarningRepo.
func NewWarningRepo(pool *pgxpool.Pool) *WarningRepo {
	return &WarningRepo{pool: pool}
}

// SaveWarning inserts a new user warning record into the database.
func (r *WarningRepo) SaveWarning(ctx context.Context, w *domain.UserWarning) error {
	const query = `
		INSERT INTO user_warnings (
			guild_id, user_id, admin_id, message_content, rule_matched,
			channel_id, ticket_id, reason, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)
		RETURNING id::text, created_at
	`
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}

	err := r.pool.QueryRow(
		ctx,
		query,
		w.GuildID,
		w.UserID,
		w.AdminID,
		w.MessageContent,
		w.RuleMatched,
		w.ChannelID,
		w.TicketID,
		w.Reason,
		w.CreatedAt,
	).Scan(&w.ID, &w.CreatedAt)
	if err != nil {
		return fmt.Errorf("saving user warning: %w", err)
	}
	return nil
}

// GetWarningsByUserID retrieves all warnings recorded against a user in a guild, latest first.
func (r *WarningRepo) GetWarningsByUserID(ctx context.Context, guildID, userID string) ([]*domain.UserWarning, error) {
	const query = `
		SELECT id::text, guild_id, user_id, admin_id, message_content,
		       COALESCE(rule_matched, ''), COALESCE(channel_id, ''),
		       COALESCE(ticket_id, ''), COALESCE(reason, ''), created_at
		FROM user_warnings
		WHERE guild_id = $1 AND user_id = $2
		ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, query, guildID, userID)
	if err != nil {
		return nil, fmt.Errorf("querying user warnings: %w", err)
	}
	defer rows.Close()

	var warnings []*domain.UserWarning
	for rows.Next() {
		w := &domain.UserWarning{}
		if err := rows.Scan(
			&w.ID,
			&w.GuildID,
			&w.UserID,
			&w.AdminID,
			&w.MessageContent,
			&w.RuleMatched,
			&w.ChannelID,
			&w.TicketID,
			&w.Reason,
			&w.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning user warning: %w", err)
		}
		warnings = append(warnings, w)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating user warnings: %w", err)
	}
	return warnings, nil
}

// GetWarningCountByUserID returns the total count of warnings issued to a user.
func (r *WarningRepo) GetWarningCountByUserID(ctx context.Context, guildID, userID string) (int, error) {
	const query = `
		SELECT COUNT(*)
		FROM user_warnings
		WHERE guild_id = $1 AND user_id = $2
	`
	var count int
	if err := r.pool.QueryRow(ctx, query, guildID, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting user warnings: %w", err)
	}
	return count, nil
}

// SaveViolation persists a pending filter violation associated with a ticket channel.
func (r *WarningRepo) SaveViolation(ctx context.Context, v *domain.FilterViolation) error {
	const query = `
		INSERT INTO filter_violations (
			guild_id, channel_id, ticket_id, user_id, message_content,
			matched_word, origin_channel, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
		RETURNING id::text, created_at
	`
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}

	err := r.pool.QueryRow(
		ctx,
		query,
		v.GuildID,
		v.ChannelID,
		v.TicketID,
		v.UserID,
		v.MessageContent,
		v.MatchedWord,
		v.OriginChannel,
		v.CreatedAt,
	).Scan(&v.ID, &v.CreatedAt)
	if err != nil {
		return fmt.Errorf("saving filter violation: %w", err)
	}
	return nil
}

// GetViolationByChannelID retrieves the filter violation record for a ticket channel.
func (r *WarningRepo) GetViolationByChannelID(ctx context.Context, guildID, channelID string) (*domain.FilterViolation, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, COALESCE(ticket_id, ''),
		       user_id, message_content, matched_word, origin_channel, created_at
		FROM filter_violations
		WHERE guild_id = $1 AND channel_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`
	v := &domain.FilterViolation{}
	err := r.pool.QueryRow(ctx, query, guildID, channelID).Scan(
		&v.ID,
		&v.GuildID,
		&v.ChannelID,
		&v.TicketID,
		&v.UserID,
		&v.MessageContent,
		&v.MatchedWord,
		&v.OriginChannel,
		&v.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying filter violation by channel: %w", err)
	}
	return v, nil
}

