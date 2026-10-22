package domain

import (
	"context"
	"errors"
	"time"

)

var (
	ErrAlreadyVerified = errors.New("member is already 18+ verified")
	ErrSelfVerify      = errors.New("staff and helpers cannot verify themselves")
	ErrInvalidInput    = errors.New("invalid user or guild identifier")
	ErrUnauthorized    = errors.New("insufficient permissions: requires Staff or Server Helper role")
)

// Verification represents a member's 18+ verification record.
type Verification struct {
	DiscordUserID string
	GuildID       string
	IsVerified    bool
	VerifiedByID  string
	VerifiedAt    time.Time
}

// VerificationRepository defines the contract for database persistence of member verifications.
type VerificationRepository interface {
	GetByUserID(ctx context.Context, guildID, userID string) (*Verification, error)
	SaveVerification(ctx context.Context, v *Verification) error
}