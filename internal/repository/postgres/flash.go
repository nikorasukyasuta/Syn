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

// FlashRepo implements domain.FlashRepository backed by PostgreSQL.
type FlashRepo struct {
	pool *pgxpool.Pool
}

// NewFlashRepo constructs a new FlashRepo.
func NewFlashRepo(pool *pgxpool.Pool) *FlashRepo {
	return &FlashRepo{pool: pool}
}

// SaveChannelConfig upserts the configuration and TTL duration for a Flash and Dash channel.
func (r *FlashRepo) SaveChannelConfig(ctx context.Context, cfg *domain.FlashChannelConfig) error {
	const query = `
		INSERT INTO flash_dash_channels (
			guild_id, channel_id, duration_sec, created_at, updated_at
		) VALUES (
			$1, $2, $3, NOW(), NOW()
		)
		ON CONFLICT (channel_id) DO UPDATE SET
			duration_sec = EXCLUDED.duration_sec,
			updated_at = NOW()
	`
	_, err := r.pool.Exec(ctx, query, cfg.GuildID, cfg.ChannelID, cfg.DurationSec)
	if err != nil {
		return fmt.Errorf("saving flash channel config: %w", err)
	}
	return nil
}

// GetChannelConfig retrieves the configuration for a specific channel.
func (r *FlashRepo) GetChannelConfig(ctx context.Context, guildID, channelID string) (*domain.FlashChannelConfig, error) {
	const query = `
		SELECT guild_id, channel_id, duration_sec, created_at, updated_at
		FROM flash_dash_channels
		WHERE guild_id = $1 AND channel_id = $2
	`
	cfg := &domain.FlashChannelConfig{}
	err := r.pool.QueryRow(ctx, query, guildID, channelID).Scan(
		&cfg.GuildID,
		&cfg.ChannelID,
		&cfg.DurationSec,
		&cfg.CreatedAt,
		&cfg.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying flash channel config: %w", err)
	}
	return cfg, nil
}

// ListChannelConfigs returns all Flash and Dash channel configurations for a guild.
func (r *FlashRepo) ListChannelConfigs(ctx context.Context, guildID string) ([]*domain.FlashChannelConfig, error) {
	const query = `
		SELECT guild_id, channel_id, duration_sec, created_at, updated_at
		FROM flash_dash_channels
		WHERE guild_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.pool.Query(ctx, query, guildID)
	if err != nil {
		return nil, fmt.Errorf("querying all flash channel configs: %w", err)
	}
	defer rows.Close()

	var list []*domain.FlashChannelConfig
	for rows.Next() {
		cfg := &domain.FlashChannelConfig{}
		if err := rows.Scan(
			&cfg.GuildID,
			&cfg.ChannelID,
			&cfg.DurationSec,
			&cfg.CreatedAt,
			&cfg.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning flash channel config: %w", err)
		}
		list = append(list, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating flash channel configs: %w", err)
	}
	return list, nil
}

// DeleteChannelConfig removes a channel from the Flash and Dash configuration.
func (r *FlashRepo) DeleteChannelConfig(ctx context.Context, guildID, channelID string) error {
	const query = `
		DELETE FROM flash_dash_channels
		WHERE guild_id = $1 AND channel_id = $2
	`
	_, err := r.pool.Exec(ctx, query, guildID, channelID)
	if err != nil {
		return fmt.Errorf("deleting flash channel config: %w", err)
	}
	return nil
}

// SavePendingMessage records a message scheduled for automatic deletion.
func (r *FlashRepo) SavePendingMessage(ctx context.Context, msg *domain.FlashMessage) error {
	const query = `
		INSERT INTO flash_dash_messages (
			guild_id, channel_id, message_id, delete_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5
		)
		ON CONFLICT (message_id) DO UPDATE SET
			delete_at = EXCLUDED.delete_at
		RETURNING id::text
	`
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}

	err := r.pool.QueryRow(
		ctx,
		query,
		msg.GuildID,
		msg.ChannelID,
		msg.MessageID,
		msg.DeleteAt,
		msg.CreatedAt,
	).Scan(&msg.ID)
	if err != nil {
		return fmt.Errorf("saving pending flash message: %w", err)
	}
	return nil
}

// DeletePendingMessage removes a message from the auto-deletion queue once deleted.
func (r *FlashRepo) DeletePendingMessage(ctx context.Context, channelID, messageID string) error {
	const query = `
		DELETE FROM flash_dash_messages
		WHERE channel_id = $1 AND message_id = $2
	`
	_, err := r.pool.Exec(ctx, query, channelID, messageID)
	if err != nil {
		return fmt.Errorf("deleting pending flash message: %w", err)
	}
	return nil
}

// ListExpiredMessages returns all messages whose delete_at timestamp has passed.
func (r *FlashRepo) ListExpiredMessages(ctx context.Context, before time.Time) ([]*domain.FlashMessage, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, message_id, delete_at, created_at
		FROM flash_dash_messages
		WHERE delete_at <= $1
		ORDER BY delete_at ASC
	`
	rows, err := r.pool.Query(ctx, query, before)
	if err != nil {
		return nil, fmt.Errorf("querying expired flash messages: %w", err)
	}
	defer rows.Close()

	var list []*domain.FlashMessage
	for rows.Next() {
		msg := &domain.FlashMessage{}
		if err := rows.Scan(
			&msg.ID,
			&msg.GuildID,
			&msg.ChannelID,
			&msg.MessageID,
			&msg.DeleteAt,
			&msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning expired flash message: %w", err)
		}
		list = append(list, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating expired flash messages: %w", err)
	}
	return list, nil
}

// ListPendingMessages returns all pending messages scheduled for deletion in the future.
func (r *FlashRepo) ListPendingMessages(ctx context.Context) ([]*domain.FlashMessage, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, message_id, delete_at, created_at
		FROM flash_dash_messages
		ORDER BY delete_at ASC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying pending flash messages: %w", err)
	}
	defer rows.Close()

	var list []*domain.FlashMessage
	for rows.Next() {
		msg := &domain.FlashMessage{}
		if err := rows.Scan(
			&msg.ID,
			&msg.GuildID,
			&msg.ChannelID,
			&msg.MessageID,
			&msg.DeleteAt,
			&msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning pending flash message: %w", err)
		}
		list = append(list, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pending flash messages: %w", err)
	}
	return list, nil
}

