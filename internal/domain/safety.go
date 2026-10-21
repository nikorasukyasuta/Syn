package domain

import (
	"context"
	"time"

)

// IncidentSeverity categorizes community safety reports.
type IncidentSeverity string

const (
	SeverityLow      IncidentSeverity = "LOW"
	SeverityMedium   IncidentSeverity = "MEDIUM"
	SeverityCritical IncidentSeverity = "CRITICAL" // e.g., Safeword violation or non-consensual behavior
)

// SafetyIncident represents an encrypted report filed by a member or staff.
type SafetyIncident struct {
	ID               string
	GuildID          string
	ReporterID       string
	SubjectID        string
	Severity         IncidentSeverity
	DetailsEncrypted []byte // AES-256-GCM encrypted payload
	CreatedAt        time.Time
}

// SafetyRepository defines persistence operations for safety and consent incidents.
type SafetyRepository interface {
	CreateIncident(ctx context.Context, incident *SafetyIncident) error
	GetIncidentsBySubject(ctx context.Context, guildID, subjectID string) ([]*SafetyIncident, error)
}