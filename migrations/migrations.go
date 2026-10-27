package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.up.sql
var migrationFiles embed.FS

// Run applies all pending database migrations in alphabetical order.
func Run(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	// 1. Ensure the schema_migrations tracking table exists
	const initTrackingQuery = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`
	if _, err := pool.Exec(ctx, initTrackingQuery); err != nil {
		return fmt.Errorf("creating schema_migrations table: %w", err)
	}

	// 2. Discover and sort all embedded migration files
	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		return fmt.Errorf("reading embedded migration files: %w", err)
	}

	var filenames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			filenames = append(filenames, entry.Name())
		}
	}
	sort.Strings(filenames)

	// 3. For each migration, check if already applied; if not, execute in transaction
	for _, filename := range filenames {
		var exists bool
		err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", filename).Scan(&exists)
		if err != nil {
			return fmt.Errorf("checking migration status for %s: %w", filename, err)
		}
		if exists {
			continue
		}

		content, err := migrationFiles.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("reading migration content for %s: %w", filename, err)
		}

		logger.Info("applying database migration", "migration", filename)

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("beginning transaction for %s: %w", filename, err)
		}

		if _, err := tx.Exec(ctx, string(content)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("executing migration %s: %w", filename, err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", filename); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("recording migration %s: %w", filename, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("committing migration %s: %w", filename, err)
		}

		logger.Info("database migration applied successfully", "migration", filename)
	}

	return nil
}

