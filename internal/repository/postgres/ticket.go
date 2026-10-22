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

// TicketRepo implements domain.TicketRepository backed by PostgreSQL.
type TicketRepo struct {
	pool *pgxpool.Pool
}

// NewTicketRepo constructs a new TicketRepo.
func NewTicketRepo(pool *pgxpool.Pool) *TicketRepo {
	return &TicketRepo{pool: pool}
}

// CreateTicket persists a new ticket record into PostgreSQL.
// If the user already has an open ticket of the same type, returns domain.ErrTicketAlreadyOpen.
func (r *TicketRepo) CreateTicket(ctx context.Context, t *domain.Ticket) error {
	const query = `
		INSERT INTO tickets (
			guild_id, channel_id, creator_id, ticket_type, status, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6
		)
		RETURNING id::text, created_at
	`
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if t.Status == "" {
		t.Status = domain.TicketStatusOpen
	}

	err := r.pool.QueryRow(
		ctx,
		query,
		t.GuildID,
		t.ChannelID,
		t.CreatorID,
		string(t.Type),
		string(t.Status),
		t.CreatedAt,
	).Scan(&t.ID, &t.CreatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrTicketAlreadyOpen
		}
		return fmt.Errorf("inserting ticket record: %w", err)
	}

	return nil
}

// GetTicketByChannelID retrieves a ticket by its Discord channel ID within a guild.
func (r *TicketRepo) GetTicketByChannelID(ctx context.Context, guildID, channelID string) (*domain.Ticket, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, creator_id, ticket_type, status,
		       COALESCE(closed_by_id, ''), COALESCE(resolution, ''), created_at, closed_at
		FROM tickets
		WHERE guild_id = $1 AND channel_id = $2
	`
	var t domain.Ticket
	var rawType string
	var rawStatus string
	var closedAt sql.NullTime

	err := r.pool.QueryRow(ctx, query, guildID, channelID).Scan(
		&t.ID,
		&t.GuildID,
		&t.ChannelID,
		&t.CreatorID,
		&rawType,
		&rawStatus,
		&t.ClosedByID,
		&t.Resolution,
		&t.CreatedAt,
		&closedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrTicketNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying ticket by channel: %w", err)
	}

	t.Type = domain.TicketType(rawType)
	t.Status = domain.TicketStatus(rawStatus)
	if closedAt.Valid {
		t.ClosedAt = &closedAt.Time
	}

	return &t, nil
}

// GetActiveTicket finds an open ticket for a given creator and ticket type.
func (r *TicketRepo) GetActiveTicket(ctx context.Context, guildID, creatorID string, ticketType domain.TicketType) (*domain.Ticket, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, creator_id, ticket_type, status,
		       COALESCE(closed_by_id, ''), COALESCE(resolution, ''), created_at, closed_at
		FROM tickets
		WHERE guild_id = $1 AND creator_id = $2 AND ticket_type = $3 AND status = 'open'
		LIMIT 1
	`
	var t domain.Ticket
	var rawType string
	var rawStatus string
	var closedAt sql.NullTime

	err := r.pool.QueryRow(ctx, query, guildID, creatorID, string(ticketType)).Scan(
		&t.ID,
		&t.GuildID,
		&t.ChannelID,
		&t.CreatorID,
		&rawType,
		&rawStatus,
		&t.ClosedByID,
		&t.Resolution,
		&t.CreatedAt,
		&closedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // No active ticket found
	}
	if err != nil {
		return nil, fmt.Errorf("querying active ticket: %w", err)
	}

	t.Type = domain.TicketType(rawType)
	t.Status = domain.TicketStatus(rawStatus)
	if closedAt.Valid {
		t.ClosedAt = &closedAt.Time
	}

	return &t, nil
}

// CloseTicket marks an active ticket as closed and records the closer and resolution.
func (r *TicketRepo) CloseTicket(ctx context.Context, guildID, channelID, closedByID, resolution string, closedAt time.Time) error {
	const query = `
		UPDATE tickets
		SET status = 'closed',
		    closed_by_id = $1,
		    resolution = $2,
		    closed_at = $3
		WHERE guild_id = $4 AND channel_id = $5 AND status = 'open'
	`
	tag, err := r.pool.Exec(ctx, query, closedByID, resolution, closedAt, guildID, channelID)
	if err != nil {
		return fmt.Errorf("closing ticket: %w", err)
	}

	if tag.RowsAffected() == 0 {
		// Distinguish between ticket not existing or ticket already closed
		ticket, getErr := r.GetTicketByChannelID(ctx, guildID, channelID)
		if getErr != nil {
			return getErr
		}
		if ticket.Status == domain.TicketStatusClosed {
			return domain.ErrTicketAlreadyClosed
		}
		return domain.ErrTicketNotFound
	}

	return nil
}

// ListOpenTicketsByCreator returns all open tickets currently owned by a user.
func (r *TicketRepo) ListOpenTicketsByCreator(ctx context.Context, guildID, creatorID string) ([]*domain.Ticket, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, creator_id, ticket_type, status,
		       COALESCE(closed_by_id, ''), COALESCE(resolution, ''), created_at, closed_at
		FROM tickets
		WHERE guild_id = $1 AND creator_id = $2 AND status = 'open'
		ORDER BY created_at ASC
	`
	rows, err := r.pool.Query(ctx, query, guildID, creatorID)
	if err != nil {
		return nil, fmt.Errorf("listing open tickets for creator: %w", err)
	}
	defer rows.Close()

	var tickets []*domain.Ticket
	for rows.Next() {
		var t domain.Ticket
		var rawType string
		var rawStatus string
		var closedAt sql.NullTime

		if err := rows.Scan(
			&t.ID,
			&t.GuildID,
			&t.ChannelID,
			&t.CreatorID,
			&rawType,
			&rawStatus,
			&t.ClosedByID,
			&t.Resolution,
			&t.CreatedAt,
			&closedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning ticket row: %w", err)
		}

		t.Type = domain.TicketType(rawType)
		t.Status = domain.TicketStatus(rawStatus)
		if closedAt.Valid {
			t.ClosedAt = &closedAt.Time
		}
		tickets = append(tickets, &t)
	}

	return tickets, rows.Err()
}

