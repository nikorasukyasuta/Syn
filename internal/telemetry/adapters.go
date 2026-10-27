package telemetry

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"syn/internal/discord/queue"
)

// pgxPoolAdapter adapts *pgxpool.Pool to telemetry.DBStatsProvider.
type pgxPoolAdapter struct {
	pool *pgxpool.Pool
}

// NewPGXPoolStatsProvider wraps a *pgxpool.Pool as a DBStatsProvider.
func NewPGXPoolStatsProvider(pool *pgxpool.Pool) DBStatsProvider {
	if pool == nil {
		return nil
	}
	return &pgxPoolAdapter{pool: pool}
}

// PoolStats retrieves live connection counts from the underlying pgxpool.Pool.
func (a *pgxPoolAdapter) PoolStats() DBPoolStats {
	if a == nil || a.pool == nil {
		return DBPoolStats{}
	}
	st := a.pool.Stat()
	return DBPoolStats{
		TotalConns:    st.TotalConns(),
		AcquiredConns: st.AcquiredConns(),
		IdleConns:     st.IdleConns(),
		MaxConns:      st.MaxConns(),
	}
}

// commandQueueAdapter adapts *queue.CommandQueue to telemetry.QueueStatsProvider.
type commandQueueAdapter struct {
	q *queue.CommandQueue
}

// NewCommandQueueStatsProvider wraps a *queue.CommandQueue as a QueueStatsProvider.
func NewCommandQueueStatsProvider(q *queue.CommandQueue) QueueStatsProvider {
	if q == nil {
		return nil
	}
	return &commandQueueAdapter{q: q}
}

// QueueStats retrieves live worker and depth metrics from the underlying CommandQueue.
func (a *commandQueueAdapter) QueueStats() QueueStatsSnapshot {
	if a == nil || a.q == nil {
		return QueueStatsSnapshot{}
	}
	st := a.q.Stats()
	return QueueStatsSnapshot{
		Depth:     st.QueueDepth,
		Capacity:  st.QueueCapacity,
		Workers:   st.WorkersCount,
		Processed: st.TotalProcessed,
		Dropped:   st.TotalDropped,
	}
}

