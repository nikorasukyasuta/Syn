package domain

import (
	"context"
	"time"
)

// AuditLog represents an immutable record of security, moderation, and safeword events.
type AuditLog struct {
	ID        string
	ActorID   string
	TargetID  string
	Action    string
	CreatedAt time.Time
}

// AuditRepository defines the contract for recording standalone security and moderation audit events.
type AuditRepository interface {
	LogAudit(ctx context.Context, actorID, targetID, action string) error
}

