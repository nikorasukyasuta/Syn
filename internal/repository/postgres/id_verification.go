package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"syn/internal/domain"
)

// IDVerificationRepo implements domain.IDVerificationRepository backed by PostgreSQL.
type IDVerificationRepo struct {
	pool *pgxpool.Pool
}

// NewIDVerificationRepo constructs a new IDVerificationRepo.
func NewIDVerificationRepo(pool *pgxpool.Pool) *IDVerificationRepo {
	return &IDVerificationRepo{pool: pool}
}

// CreateRequest persists a new ID verification request session.
func (r *IDVerificationRepo) CreateRequest(ctx context.Context, req *domain.IDVerificationRequest) error {
	const query = `
		INSERT INTO id_verification_requests (
			guild_id, channel_id, user_id, status, created_at
		) VALUES (
			$1, $2, $3, $4, $5
		)
		RETURNING id::text, created_at
	`
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now().UTC()
	}
	if req.Status == "" {
		req.Status = domain.IDVerificationStatusPending
	}

	err := r.pool.QueryRow(
		ctx,
		query,
		req.GuildID,
		req.ChannelID,
		req.UserID,
		string(req.Status),
		req.CreatedAt,
	).Scan(&req.ID, &req.CreatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrIDVerifyRequestAlreadyOpen
		}
		return fmt.Errorf("inserting id verification request: %w", err)
	}

	return nil
}

// GetRequestByChannelID retrieves an ID verification request by its Discord channel ID.
func (r *IDVerificationRepo) GetRequestByChannelID(ctx context.Context, guildID, channelID string) (*domain.IDVerificationRequest, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, user_id, status,
		       COALESCE(reviewed_by_id, ''), COALESCE(review_reason, ''), created_at, reviewed_at
		FROM id_verification_requests
		WHERE guild_id = $1 AND channel_id = $2
	`
	var req domain.IDVerificationRequest
	var rawStatus string
	var reviewedAt sql.NullTime

	err := r.pool.QueryRow(ctx, query, guildID, channelID).Scan(
		&req.ID,
		&req.GuildID,
		&req.ChannelID,
		&req.UserID,
		&rawStatus,
		&req.ReviewedByID,
		&req.ReviewReason,
		&req.CreatedAt,
		&reviewedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrIDVerifyRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying id verification request by channel: %w", err)
	}

	req.Status = domain.IDVerificationStatus(rawStatus)
	if reviewedAt.Valid {
		t := reviewedAt.Time
		req.ReviewedAt = &t
	}

	return &req, nil
}

// GetPendingRequestByUser retrieves an active pending ID verification request for a user.
func (r *IDVerificationRepo) GetPendingRequestByUser(ctx context.Context, guildID, userID string) (*domain.IDVerificationRequest, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, user_id, status,
		       COALESCE(reviewed_by_id, ''), COALESCE(review_reason, ''), created_at, reviewed_at
		FROM id_verification_requests
		WHERE guild_id = $1 AND user_id = $2 AND status = 'pending'
		ORDER BY created_at DESC
		LIMIT 1
	`
	var req domain.IDVerificationRequest
	var rawStatus string
	var reviewedAt sql.NullTime

	err := r.pool.QueryRow(ctx, query, guildID, userID).Scan(
		&req.ID,
		&req.GuildID,
		&req.ChannelID,
		&req.UserID,
		&rawStatus,
		&req.ReviewedByID,
		&req.ReviewReason,
		&req.CreatedAt,
		&reviewedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying pending id verification request: %w", err)
	}

	req.Status = domain.IDVerificationStatus(rawStatus)
	if reviewedAt.Valid {
		t := reviewedAt.Time
		req.ReviewedAt = &t
	}

	return &req, nil
}

// UpdateRequestStatus updates the review status, reviewer, and reason for an ID verification request.
func (r *IDVerificationRepo) UpdateRequestStatus(
	ctx context.Context,
	id string,
	status domain.IDVerificationStatus,
	reviewerID, reason string,
	reviewedAt time.Time,
) error {
	const query = `
		UPDATE id_verification_requests
		SET status = $2,
		    reviewed_by_id = $3,
		    review_reason = $4,
		    reviewed_at = $5
		WHERE id = $1::uuid AND status = 'pending'
	`
	tag, err := r.pool.Exec(ctx, query, id, string(status), reviewerID, reason, reviewedAt)
	if err != nil {
		return fmt.Errorf("updating id verification request status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrIDVerifyAlreadyReviewed
	}
	return nil
}

