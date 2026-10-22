package postgres

import (
	"context"
	"errors"
	"fmt"
	"syn/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReactionRoleRepo implements domain.ReactionRoleRepository using pgxpool.
type ReactionRoleRepo struct {
	pool *pgxpool.Pool
}

// NewReactionRoleRepo constructs a new PostgreSQL reaction role repository.
func NewReactionRoleRepo(pool *pgxpool.Pool) *ReactionRoleRepo {
	return &ReactionRoleRepo{pool: pool}
}

// UpsertMapping saves a reaction role configuration and records an audit log atomically.
func (r *ReactionRoleRepo) UpsertMapping(ctx context.Context, rr *domain.ReactionRole) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		const query = `
			INSERT INTO reaction_roles (
				guild_id, channel_id, message_id, emoji_api_name, emoji_display,
				role_id, description, requires_verified, created_by_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (message_id, emoji_api_name) DO UPDATE
			SET role_id = EXCLUDED.role_id,
			    emoji_display = EXCLUDED.emoji_display,
			    description = EXCLUDED.description,
			    requires_verified = EXCLUDED.requires_verified,
			    created_by_id = EXCLUDED.created_by_id
		`
		_, err := tx.Exec(ctx, query,
			rr.GuildID, rr.ChannelID, rr.MessageID, rr.EmojiAPIName, rr.EmojiDisplay,
			rr.RoleID, rr.Description, rr.RequiresVerified, rr.CreatedByID,
		)
		if err != nil {
			return fmt.Errorf("upserting reaction role: %w", err)
		}

		const auditQuery = `
			INSERT INTO audit_logs (actor_id, target_id, action)
			VALUES ($1, $2, 'BIND_REACTION_ROLE')
		`
		if _, err := tx.Exec(ctx, auditQuery, rr.CreatedByID, rr.RoleID); err != nil {
			return fmt.Errorf("inserting reaction role audit log: %w", err)
		}
		return nil
	})
}

// GetMapping fetches a single reaction role configuration for a given message and emoji.
// It supports exact matches as well as parsed custom emoji formats.
func (r *ReactionRoleRepo) GetMapping(ctx context.Context, guildID, messageID, emojiAPIName string) (*domain.ReactionRole, error) {
	const query = `
		SELECT id, guild_id, channel_id, message_id, emoji_api_name, emoji_display,
		       role_id, description, requires_verified, created_by_id, created_at
		FROM reaction_roles
		WHERE guild_id = $1 AND message_id = $2 AND (
			emoji_api_name = $3 OR 
			emoji_api_name = split_part($3, ':', 1) OR 
			(split_part($3, ':', 2) != '' AND emoji_api_name = split_part($3, ':', 2)) OR
			emoji_api_name = '<:' || $3 || '>' OR
			emoji_api_name = '<a:' || $3 || '>'
		)
		LIMIT 1
	`
	var rr domain.ReactionRole
	err := r.pool.QueryRow(ctx, query, guildID, messageID, emojiAPIName).Scan(
		&rr.ID, &rr.GuildID, &rr.ChannelID, &rr.MessageID,
		&rr.EmojiAPIName, &rr.EmojiDisplay, &rr.RoleID, &rr.Description,
		&rr.RequiresVerified, &rr.CreatedByID, &rr.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying reaction role: %w", err)
	}
	return &rr, nil
}

// ListByMessage returns all reaction roles bound to a specific panel message in chronological order.
func (r *ReactionRoleRepo) ListByMessage(ctx context.Context, guildID, messageID string) ([]*domain.ReactionRole, error) {
	const query = `
		SELECT id, guild_id, channel_id, message_id, emoji_api_name, emoji_display,
		       role_id, description, requires_verified, created_by_id, created_at
		FROM reaction_roles
		WHERE guild_id = $1 AND message_id = $2
		ORDER BY created_at ASC
	`
	rows, err := r.pool.Query(ctx, query, guildID, messageID)
	if err != nil {
		return nil, fmt.Errorf("listing reaction roles for message: %w", err)
	}
	defer rows.Close()

	var results []*domain.ReactionRole
	for rows.Next() {
		var rr domain.ReactionRole
		if err := rows.Scan(
			&rr.ID, &rr.GuildID, &rr.ChannelID, &rr.MessageID,
			&rr.EmojiAPIName, &rr.EmojiDisplay, &rr.RoleID, &rr.Description,
			&rr.RequiresVerified, &rr.CreatedByID, &rr.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning reaction role row: %w", err)
		}
		results = append(results, &rr)
	}
	return results, rows.Err()
}

// DeleteMapping removes a reaction role configuration from a message.
func (r *ReactionRoleRepo) DeleteMapping(ctx context.Context, guildID, messageID, emojiAPIName string) error {
	const query = `
		DELETE FROM reaction_roles
		WHERE guild_id = $1 AND message_id = $2 AND (
			emoji_api_name = $3 OR 
			emoji_api_name = split_part($3, ':', 1) OR 
			(split_part($3, ':', 2) != '' AND emoji_api_name = split_part($3, ':', 2)) OR
			emoji_api_name = '<:' || $3 || '>' OR
			emoji_api_name = '<a:' || $3 || '>'
		)
	`
	_, err := r.pool.Exec(ctx, query, guildID, messageID, emojiAPIName)
	return err
}

// SavePanel inserts or updates the visual design and local EC2 filenames for a reaction role panel.
func (r *ReactionRoleRepo) SavePanel(ctx context.Context, p *domain.ReactionPanel) error {
	const query = `
		INSERT INTO reaction_panels (
			message_id, guild_id, channel_id, title, intro,
			color_hex, banner_file, thumbnail_file, created_by_id, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (message_id) DO UPDATE
		SET title = EXCLUDED.title,
		    intro = EXCLUDED.intro,
		    color_hex = EXCLUDED.color_hex,
		    banner_file = EXCLUDED.banner_file,
		    thumbnail_file = EXCLUDED.thumbnail_file,
		    updated_at = NOW()
	`
	_, err := r.pool.Exec(ctx, query,
		p.MessageID, p.GuildID, p.ChannelID, p.Title, p.Intro,
		p.ColorHex, p.BannerFile, p.ThumbnailFile, p.CreatedByID,
	)
	if err != nil {
		return fmt.Errorf("saving reaction panel metadata: %w", err)
	}
	return nil
}

// GetPanel retrieves the visual design and local EC2 filenames for a specific reaction role panel.
func (r *ReactionRoleRepo) GetPanel(ctx context.Context, guildID, messageID string) (*domain.ReactionPanel, error) {
	const query = `
		SELECT message_id, guild_id, channel_id, title, intro,
		       color_hex, banner_file, thumbnail_file, created_by_id, updated_at
		FROM reaction_panels
		WHERE guild_id = $1 AND message_id = $2
	`
	var p domain.ReactionPanel
	err := r.pool.QueryRow(ctx, query, guildID, messageID).Scan(
		&p.MessageID, &p.GuildID, &p.ChannelID, &p.Title, &p.Intro,
		&p.ColorHex, &p.BannerFile, &p.ThumbnailFile, &p.CreatedByID, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying reaction panel metadata: %w", err)
	}
	return &p, nil
}

// DeletePanel removes the visual configuration for a reaction role panel.
func (r *ReactionRoleRepo) DeletePanel(ctx context.Context, guildID, messageID string) error {
	const query = `DELETE FROM reaction_panels WHERE guild_id = $1 AND message_id = $2`
	_, err := r.pool.Exec(ctx, query, guildID, messageID)
	return err
}
