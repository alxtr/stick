package mongodb

import (
	"fmt"
	"time"

	domain "stick/internal/core"
	"stick/internal/outbox"
)

type stickDocument struct {
	ID          string     `bson:"_id"`
	Name        string     `bson:"name"`
	Version     int64      `bson:"version"`
	HolderSub   *string    `bson:"holder_sub,omitempty"`
	HolderName  *string    `bson:"holder_name,omitempty"`
	HolderEmail *string    `bson:"holder_email,omitempty"`
	ClaimedAt   *time.Time `bson:"claimed_at,omitempty"`
	Reason      *string    `bson:"reason,omitempty"`
	ArchivedAt  *time.Time `bson:"archived_at,omitempty"`
}

type sessionDocument struct {
	ID          int64      `bson:"_id"`
	StickID     string     `bson:"stick_id"`
	HolderSub   string     `bson:"holder_sub"`
	HolderName  string     `bson:"holder_name"`
	HolderEmail string     `bson:"holder_email"`
	Reason      string     `bson:"reason"`
	ClaimedAt   time.Time  `bson:"claimed_at"`
	Active      bool       `bson:"active"`
	ReleasedAt  *time.Time `bson:"released_at,omitempty"`
}

type subscriptionDocument struct {
	StickID         string `bson:"stick_id"`
	UserSub         string `bson:"user_sub"`
	UserName        string `bson:"user_name"`
	UserEmail       string `bson:"user_email"`
	GenerationToken string `bson:"generation_token"`
}

type outboxDocument struct {
	ID                          int64      `bson:"_id"`
	StickID                     string     `bson:"stick_id"`
	StickName                   string     `bson:"stick_name"`
	HolderName                  string     `bson:"holder_name"`
	HolderEmail                 string     `bson:"holder_email"`
	HeldSince                   time.Time  `bson:"held_since"`
	ReleasedAt                  time.Time  `bson:"released_at"`
	RecipientSub                string     `bson:"recipient_sub"`
	RecipientName               string     `bson:"recipient_name"`
	RecipientEmail              string     `bson:"recipient_email"`
	SubscriptionGenerationToken string     `bson:"subscription_generation_token"`
	Status                      string     `bson:"status"`
	Attempts                    int64      `bson:"attempts"`
	NextAttemptAt               *time.Time `bson:"next_attempt_at,omitempty"`
	ClaimToken                  *string    `bson:"claim_token,omitempty"`
	ClaimedAt                   *time.Time `bson:"claimed_at,omitempty"`
	LastAttemptAt               *time.Time `bson:"last_attempt_at,omitempty"`
	DeliveredAt                 *time.Time `bson:"delivered_at,omitempty"`
	LastError                   *string    `bson:"last_error,omitempty"`
	CreatedAt                   time.Time  `bson:"created_at"`
}

type counterDocument struct {
	ID    string `bson:"_id"`
	Value int64  `bson:"value"`
}

func stickFromDocument(doc stickDocument) domain.Stick {
	stick := domain.Stick{ID: doc.ID, Name: doc.Name, Version: doc.Version, ArchivedAt: doc.ArchivedAt}
	if doc.HolderSub != nil {
		stick.Holder = &domain.Holder{
			Sub:       *doc.HolderSub,
			Name:      stringValue(doc.HolderName),
			Email:     stringValue(doc.HolderEmail),
			ClaimedAt: timeValue(doc.ClaimedAt),
			Reason:    stringValue(doc.Reason),
		}
	}
	return stick
}

func stickToDocument(stick domain.Stick) stickDocument {
	doc := stickDocument{ID: stick.ID, Name: stick.Name, Version: stick.Version, ArchivedAt: stick.ArchivedAt}
	if stick.Holder != nil {
		sub, name, email, reason := stick.Holder.Sub, stick.Holder.Name, stick.Holder.Email, stick.Holder.Reason
		doc.HolderSub, doc.HolderName, doc.HolderEmail = &sub, &name, &email
		doc.ClaimedAt, doc.Reason = &stick.Holder.ClaimedAt, &reason
	}
	return doc
}

func sessionFromDocument(doc sessionDocument) domain.Session {
	return domain.Session{
		ID:          doc.ID,
		StickID:     doc.StickID,
		HolderSub:   doc.HolderSub,
		HolderName:  doc.HolderName,
		HolderEmail: doc.HolderEmail,
		Reason:      doc.Reason,
		ClaimedAt:   doc.ClaimedAt,
		ReleasedAt:  doc.ReleasedAt,
	}
}

func deliveryFromDocument(doc outboxDocument) (outbox.Delivery, error) {
	if doc.Attempts < 0 || uint64(doc.Attempts) > uint64(maxInt()) {
		return outbox.Delivery{}, fmt.Errorf("notification %d has invalid attempt count %d", doc.ID, doc.Attempts)
	}
	return outbox.Delivery{
		ID:                          doc.ID,
		StickID:                     doc.StickID,
		StickName:                   doc.StickName,
		HolderName:                  doc.HolderName,
		HolderEmail:                 doc.HolderEmail,
		HeldSince:                   doc.HeldSince,
		ReleasedAt:                  doc.ReleasedAt,
		RecipientSub:                doc.RecipientSub,
		RecipientName:               doc.RecipientName,
		RecipientEmail:              doc.RecipientEmail,
		SubscriptionGenerationToken: doc.SubscriptionGenerationToken,
		Attempts:                    int(doc.Attempts),
		ClaimToken:                  stringValue(doc.ClaimToken),
	}, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func maxInt() int { return int(^uint(0) >> 1) }
