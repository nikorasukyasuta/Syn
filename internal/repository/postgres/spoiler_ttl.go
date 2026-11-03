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

// SpoilerTTLRepo implements domain.SpoilerTTLRepository backed by PostgreSQL.
type SpoilerTTLRepo struct {
	pool *pgxpool.Pool
}

// NewSpoilerTTLRepo constructs a new SpoilerTTLRepo.
func NewSpoilerTTLRepo(pool *pgxpool.Pool) *SpoilerTTLRepo {
	return &SpoilerTTLRepo{pool: pool}
}

// SaveUserSetting upserts a user's personal spoilered photo TTL configuration.
func (r *SpoilerTTLRepo) SaveUserSetting(ctx context.Context, setting *domain.UserSpoilerTTLSetting) error {
	const query = `
		INSERT INTO user_spoiler_ttl_settings (
			guild_id, user_id, duration_sec, updated_at
		) VALUES (
			$1, $2, $3, NOW()
		)
		ON CONFLICT (guild_id, user_id) DO UPDATE SET
			duration_sec = EXCLUDED.duration_sec,
			updated_at = NOW()
	`
	_, err := r.pool.Exec(ctx, query, setting.GuildID, setting.UserID, setting.DurationSec)
	if err != nil {
		return fmt.Errorf("saving user spoiler ttl setting: %w", err)
	}
	return nil
}

// GetUserSetting retrieves a user's personal spoilered photo TTL configuration.
func (r *SpoilerTTLRepo) GetUserSetting(ctx context.Context, guildID, userID string) (*domain.UserSpoilerTTLSetting, error) {
	const query = `
		SELECT guild_id, user_id, duration_sec, updated_at
		FROM user_spoiler_ttl_settings
		WHERE guild_id = $1 AND user_id = $2
	`
	setting := &domain.UserSpoilerTTLSetting{}
	err := r.pool.QueryRow(ctx, query, guildID, userID).Scan(
		&setting.GuildID,
		&setting.UserID,
		&setting.DurationSec,
		&setting.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("querying user spoiler ttl setting: %w", err)
	}
	return setting, nil
}

// ListUserSettings returns all user spoilered photo TTL configurations for a guild.
func (r *SpoilerTTLRepo) ListUserSettings(ctx context.Context, guildID string) ([]*domain.UserSpoilerTTLSetting, error) {
	const query = `
		SELECT guild_id, user_id, duration_sec, updated_at
		FROM user_spoiler_ttl_settings
		WHERE guild_id = $1
		ORDER BY updated_at DESC
	`
	rows, err := r.pool.Query(ctx, query, guildID)
	if err != nil {
		return nil, fmt.Errorf("querying user spoiler ttl settings: %w", err)
	}
	defer rows.Close()

	var list []*domain.UserSpoilerTTLSetting
	for rows.Next() {
		setting := &domain.UserSpoilerTTLSetting{}
		if err := rows.Scan(
			&setting.GuildID,
			&setting.UserID,
			&setting.DurationSec,
			&setting.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning user spoiler ttl setting: %w", err)
		}
		list = append(list, setting)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating user spoiler ttl settings: %w", err)
	}
	return list, nil
}

// DeleteUserSetting removes a user's personal spoilered photo TTL configuration.
func (r *SpoilerTTLRepo) DeleteUserSetting(ctx context.Context, guildID, userID string) error {
	const query = `
		DELETE FROM user_spoiler_ttl_settings
		WHERE guild_id = $1 AND user_id = $2
	`
	_, err := r.pool.Exec(ctx, query, guildID, userID)
	if err != nil {
		return fmt.Errorf("deleting user spoiler ttl setting: %w", err)
	}
	return nil
}

// SavePendingMessage records a spoilered photo message scheduled for automatic deletion.
func (r *SpoilerTTLRepo) SavePendingMessage(ctx context.Context, msg *domain.UserSpoilerMessage) error {
	const query = `
		INSERT INTO user_spoiler_messages (
			guild_id, channel_id, message_id, user_id, delete_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6
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
		msg.UserID,
		msg.DeleteAt,
		msg.CreatedAt,
	).Scan(&msg.ID)
	if err != nil {
		return fmt.Errorf("saving pending user spoiler message: %w", err)
	}
	return nil
}

// DeletePendingMessage removes a message from the auto-deletion queue once deleted.
func (r *SpoilerTTLRepo) DeletePendingMessage(ctx context.Context, channelID, messageID string) error {
	const query = `
		DELETE FROM user_spoiler_messages
		WHERE channel_id = $1 AND message_id = $2
	`
	_, err := r.pool.Exec(ctx, query, channelID, messageID)
	if err != nil {
		return fmt.Errorf("deleting pending user spoiler message: %w", err)
	}
	return nil
}

// ListExpiredMessages returns all spoiler messages whose delete_at timestamp has passed.
func (r *SpoilerTTLRepo) ListExpiredMessages(ctx context.Context, before time.Time) ([]*domain.UserSpoilerMessage, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, message_id, user_id, delete_at, created_at
		FROM user_spoiler_messages
		WHERE delete_at <= $1
		ORDER BY delete_at ASC
	`
	rows, err := r.pool.Query(ctx, query, before)
	if err != nil {
		return nil, fmt.Errorf("querying expired user spoiler messages: %w", err)
	}
	defer rows.Close()

	var list []*domain.UserSpoilerMessage
	for rows.Next() {
		msg := &domain.UserSpoilerMessage{}
		if err := rows.Scan(
			&msg.ID,
			&msg.GuildID,
			&msg.ChannelID,
			&msg.MessageID,
			&msg.UserID,
			&msg.DeleteAt,
			&msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning expired user spoiler message: %w", err)
		}
		list = append(list, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating expired user spoiler messages: %w", err)
	}
	return list, nil
}

// ListPendingMessages returns all pending spoiler messages scheduled for deletion.
func (r *SpoilerTTLRepo) ListPendingMessages(ctx context.Context) ([]*domain.UserSpoilerMessage, error) {
	const query = `
		SELECT id::text, guild_id, channel_id, message_id, user_id, delete_at, created_at
		FROM user_spoiler_messages
		ORDER BY delete_at ASC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying pending user spoiler messages: %w", err)
	}
	defer rows.Close()

	var list []*domain.UserSpoilerMessage
	for rows.Next() {
		msg := &domain.UserSpoilerMessage{}
		if err := rows.Scan(
			&msg.ID,
			&msg.GuildID,
			&msg.ChannelID,
			&msg.MessageID,
			&msg.UserID,
			&msg.DeleteAt,
			&msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning pending user spoiler message: %w", err)
		}
		list = append(list, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pending user spoiler messages: %w", err)
	}
	return list, nil
}
