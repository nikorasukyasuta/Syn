package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditRepo implements domain.AuditRepository using PostgreSQL.
type AuditRepo struct {
	pool *pgxpool.Pool
}

// NewAuditRepo constructs a new PostgreSQL Audit repository.
func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
	return &AuditRepo{pool: pool}
}

// LogAudit records standalone security, safeword, or moderation events into the audit_logs table.
func (r *AuditRepo) LogAudit(ctx context.Context, actorID, targetID, action string) error {
	const query = `INSERT INTO audit_logs (actor_id, target_id, action) VALUES ($1, $2, $3)`
	_, err := r.pool.Exec(ctx, query, actorID, targetID, action)
	return err
}

