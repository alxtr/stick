package outbox

import (
	"context"
	"errors"
	"time"
)

// ErrClaimLost means a delivery was reclaimed before an older worker could
// record its result.
var ErrClaimLost = errors.New("notification claim lost")

// Delivery is an immutable notification snapshot plus its durable attempt metadata.
type Delivery struct {
	ID                          int64
	StickID                     string
	StickName                   string
	HolderName                  string
	HolderEmail                 string
	HeldSince                   time.Time
	ReleasedAt                  time.Time
	RecipientSub                string
	RecipientName               string
	RecipientEmail              string
	SubscriptionGenerationToken string
	Attempts                    int
	ClaimToken                  string
}

// Store is the durable persistence contract consumed by Worker.
type Store interface {
	ClaimNotification(ctx context.Context, now, staleBefore time.Time, claimToken string) (*Delivery, error)
	MarkNotificationSucceeded(ctx context.Context, delivery Delivery, deliveredAt time.Time) error
	MarkNotificationFailed(ctx context.Context, delivery Delivery, nextAttemptAt time.Time, failure string) error
}
