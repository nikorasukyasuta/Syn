package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"syn/internal/domain"
)

// GetPanelPost retrieves a specific Ticket Hub post configuration for a guild.
func (r *TicketRepo) GetPanelPost(ctx context.Context, guildID string, panelType domain.TicketPanelType) (*domain.TicketPanelPost, error) {
	const query = `
		SELECT guild_id, panel_type, channel_id, message_id, title, description,
		       color, button_label, button_emoji, button_style, display_order, updated_at
		FROM ticket_panel_posts
		WHERE guild_id = $1 AND panel_type = $2
	`
	var p domain.TicketPanelPost
	var rawType string

	err := r.pool.QueryRow(ctx, query, guildID, string(panelType)).Scan(
		&p.GuildID,
		&rawType,
		&p.ChannelID,
		&p.MessageID,
		&p.Title,
		&p.Description,
		&p.Color,
		&p.ButtonLabel,
		&p.ButtonEmoji,
		&p.ButtonStyle,
		&p.DisplayOrder,
		&p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying ticket panel post: %w", err)
	}

	p.PanelType = domain.TicketPanelType(rawType)
	p.CustomID = p.PanelType.CustomID()
	return &p, nil
}

// ListPanelPosts returns all stored Ticket Hub posts for a guild ordered by display_order.
func (r *TicketRepo) ListPanelPosts(ctx context.Context, guildID string) ([]*domain.TicketPanelPost, error) {
	const query = `
		SELECT guild_id, panel_type, channel_id, message_id, title, description,
		       color, button_label, button_emoji, button_style, display_order, updated_at
		FROM ticket_panel_posts
		WHERE guild_id = $1
		ORDER BY display_order ASC
	`
	rows, err := r.pool.Query(ctx, query, guildID)
	if err != nil {
		return nil, fmt.Errorf("listing ticket panel posts: %w", err)
	}
	defer rows.Close()

	var posts []*domain.TicketPanelPost
	for rows.Next() {
		var p domain.TicketPanelPost
		var rawType string
		if err := rows.Scan(
			&p.GuildID,
			&rawType,
			&p.ChannelID,
			&p.MessageID,
			&p.Title,
			&p.Description,
			&p.Color,
			&p.ButtonLabel,
			&p.ButtonEmoji,
			&p.ButtonStyle,
			&p.DisplayOrder,
			&p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning ticket panel post row: %w", err)
		}
		p.PanelType = domain.TicketPanelType(rawType)
		p.CustomID = p.PanelType.CustomID()
		posts = append(posts, &p)
	}

	return posts, rows.Err()
}

// SavePanelPost upserts a Ticket Hub post record in PostgreSQL.
func (r *TicketRepo) SavePanelPost(ctx context.Context, post *domain.TicketPanelPost) error {
	const query = `
		INSERT INTO ticket_panel_posts (
			guild_id, panel_type, channel_id, message_id, title, description,
			color, button_label, button_emoji, button_style, display_order, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW()
		)
		ON CONFLICT (guild_id, panel_type) DO UPDATE
		SET channel_id = EXCLUDED.channel_id,
		    message_id = EXCLUDED.message_id,
		    title = EXCLUDED.title,
		    description = EXCLUDED.description,
		    color = EXCLUDED.color,
		    button_label = EXCLUDED.button_label,
		    button_emoji = EXCLUDED.button_emoji,
		    button_style = EXCLUDED.button_style,
		    display_order = EXCLUDED.display_order,
		    updated_at = NOW()
	`
	_, err := r.pool.Exec(
		ctx,
		query,
		post.GuildID,
		string(post.PanelType),
		post.ChannelID,
		post.MessageID,
		post.Title,
		post.Description,
		post.Color,
		post.ButtonLabel,
		post.ButtonEmoji,
		post.ButtonStyle,
		post.DisplayOrder,
	)
	if err != nil {
		return fmt.Errorf("saving ticket panel post: %w", err)
	}
	return nil
}

