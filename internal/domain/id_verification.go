package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrIDVerifyRequestAlreadyOpen = errors.New("you already have a pending ID verification request")
	ErrIDVerifyRequestNotFound    = errors.New("active ID verification request not found")
	ErrIDVerifyAlreadyReviewed    = errors.New("this ID verification request has already been reviewed")
	ErrNotVerified                = errors.New("member is not currently 18+ verified")
)

// IDVerificationStatus represents the review state of an ID verification session.
type IDVerificationStatus string

const (
	IDVerificationStatusPending  IDVerificationStatus = "pending"
	IDVerificationStatusApproved IDVerificationStatus = "approved"
	IDVerificationStatusDenied   IDVerificationStatus = "denied"
	IDVerificationStatusCanceled IDVerificationStatus = "canceled"
)

// IDVerificationRequest tracks an isolated 18+ ID verification channel and staff review.
type IDVerificationRequest struct {
	ID           string
	GuildID      string
	ChannelID    string
	UserID       string
	Status       IDVerificationStatus
	ReviewedByID string
	ReviewReason string
	CreatedAt    time.Time
	ReviewedAt   *time.Time
}

// IDVerificationRepository defines persistence operations for the dedicated ID verification flow.
type IDVerificationRepository interface {
	CreateRequest(ctx context.Context, req *IDVerificationRequest) error
	GetRequestByChannelID(ctx context.Context, guildID, channelID string) (*IDVerificationRequest, error)
	GetPendingRequestByUser(ctx context.Context, guildID, userID string) (*IDVerificationRequest, error)
	UpdateRequestStatus(ctx context.Context, id string, status IDVerificationStatus, reviewerID, reason string, reviewedAt time.Time) error
}

