package mongodb

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	domain "stick/internal/core"
	"stick/internal/outbox"
)

func TestStickDocumentRoundTripPreservesOptionalState(t *testing.T) {
	claimedAt := time.Date(2040, time.January, 2, 3, 4, 5, 6_000_000, time.UTC)
	archivedAt := claimedAt.Add(time.Hour)
	want := domain.Stick{
		ID:         "stick-1",
		Name:       "Production",
		Version:    7,
		ArchivedAt: &archivedAt,
		Holder: &domain.Holder{
			Sub:       "holder-1",
			Name:      "Alice",
			Email:     "alice@example.com",
			ClaimedAt: claimedAt,
			Reason:    "deploying",
		},
	}

	encoded, err := bson.Marshal(stickToDocument(want))
	if err != nil {
		t.Fatal(err)
	}
	var document stickDocument
	if err := bson.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if got := stickFromDocument(document); !reflect.DeepEqual(got, want) {
		t.Fatalf("stick round trip = %+v, want %+v", got, want)
	}
}

func TestStickDocumentOmitsAbsentOptionalState(t *testing.T) {
	document, err := bson.Marshal(stickToDocument(domain.Stick{ID: "stick-1", Name: "Available", Version: 1}))
	if err != nil {
		t.Fatal(err)
	}
	var raw bson.M
	if err := bson.Unmarshal(document, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"holder_sub", "holder_name", "holder_email", "claimed_at", "reason", "archived_at"} {
		if _, ok := raw[field]; ok {
			t.Errorf("available stick encoded optional field %q", field)
		}
	}
}

func TestSessionFromDocument(t *testing.T) {
	releasedAt := time.Date(2040, time.February, 3, 4, 5, 6, 0, time.UTC)
	want := domain.Session{
		ID:          12,
		StickID:     "stick-1",
		HolderSub:   "holder-1",
		HolderName:  "Alice",
		HolderEmail: "alice@example.com",
		Reason:      "deploying",
		ClaimedAt:   releasedAt.Add(-time.Hour),
		ReleasedAt:  &releasedAt,
	}
	got := sessionFromDocument(sessionDocument{
		ID:          want.ID,
		StickID:     want.StickID,
		HolderSub:   want.HolderSub,
		HolderName:  want.HolderName,
		HolderEmail: want.HolderEmail,
		Reason:      want.Reason,
		ClaimedAt:   want.ClaimedAt,
		ReleasedAt:  want.ReleasedAt,
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session conversion = %+v, want %+v", got, want)
	}
}

func TestDeliveryFromDocument(t *testing.T) {
	claimedAt := time.Date(2040, time.March, 4, 5, 6, 7, 0, time.UTC)
	deliveredAt := claimedAt.Add(time.Hour)
	claimToken := "claim-token"
	want := outbox.Delivery{
		ID:                          3,
		StickID:                     "stick-1",
		StickName:                   "Production",
		HolderName:                  "Alice",
		HolderEmail:                 "alice@example.com",
		HeldSince:                   claimedAt,
		ReleasedAt:                  deliveredAt,
		RecipientSub:                "watcher-1",
		RecipientName:               "Bob",
		RecipientEmail:              "bob@example.com",
		SubscriptionGenerationToken: "generation-1",
		Attempts:                    2,
		ClaimToken:                  claimToken,
	}

	got, err := deliveryFromDocument(outboxDocument{
		ID:                          want.ID,
		StickID:                     want.StickID,
		StickName:                   want.StickName,
		HolderName:                  want.HolderName,
		HolderEmail:                 want.HolderEmail,
		HeldSince:                   want.HeldSince,
		ReleasedAt:                  want.ReleasedAt,
		RecipientSub:                want.RecipientSub,
		RecipientName:               want.RecipientName,
		RecipientEmail:              want.RecipientEmail,
		SubscriptionGenerationToken: want.SubscriptionGenerationToken,
		Attempts:                    int64(want.Attempts),
		ClaimToken:                  &claimToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivery conversion = %+v, want %+v", got, want)
	}
}

func TestDeliveryFromDocumentRejectsNegativeAttempts(t *testing.T) {
	if _, err := deliveryFromDocument(outboxDocument{ID: 9, Attempts: -1}); err == nil {
		t.Fatal("negative attempts unexpectedly accepted")
	}
}
