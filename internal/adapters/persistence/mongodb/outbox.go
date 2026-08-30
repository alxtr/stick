package mongodb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"stick/internal/outbox"
)

// ClaimNotification claims the next deliverable notification atomically.
func (s *Store) ClaimNotification(ctx context.Context, now, staleBefore time.Time, claimToken string) (*outbox.Delivery, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	nextAttemptReady := bson.A{
		bson.D{{Key: "next_attempt_at", Value: bson.D{{Key: "$exists", Value: false}}}},
		bson.D{{Key: "next_attempt_at", Value: nil}},
		bson.D{{Key: "next_attempt_at", Value: bson.D{{Key: "$lte", Value: now}}}},
	}
	filter := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{"pending", "failed"}}}}, {Key: "$or", Value: nextAttemptReady}},
		bson.D{{Key: "status", Value: "in_progress"}, {Key: "claimed_at", Value: bson.D{{Key: "$lte", Value: staleBefore}}}},
	}}}
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "status", Value: "in_progress"},
			{Key: "claim_token", Value: claimToken},
			{Key: "claimed_at", Value: now},
			{Key: "last_attempt_at", Value: now},
		}},
		{Key: "$inc", Value: bson.D{{Key: "attempts", Value: int64(1)}}},
	}

	var doc outboxDocument
	err := s.db.Collection(outboxCollection).FindOneAndUpdate(ctx, filter, update,
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "_id", Value: 1}}).SetReturnDocument(options.After),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim notification: %w", err)
	}
	delivery, err := deliveryFromDocument(doc)
	if err != nil {
		return nil, fmt.Errorf("claim notification: %w", err)
	}
	return &delivery, nil
}

// MarkNotificationSucceeded records successful delivery and removes only the
// subscription generation captured by this delivery.
func (s *Store) MarkNotificationSucceeded(ctx context.Context, delivery outbox.Delivery, deliveredAt time.Time) error {
	err := s.runInTransaction(ctx, func(txCtx mongo.SessionContext) error {
		result, err := s.db.Collection(outboxCollection).UpdateOne(txCtx,
			bson.D{{Key: "_id", Value: delivery.ID}, {Key: "status", Value: "in_progress"}, {Key: "claim_token", Value: delivery.ClaimToken}},
			bson.D{
				{Key: "$set", Value: bson.D{{Key: "status", Value: "succeeded"}, {Key: "delivered_at", Value: deliveredAt}}},
				{Key: "$unset", Value: bson.D{{Key: "next_attempt_at", Value: ""}, {Key: "claim_token", Value: ""}, {Key: "claimed_at", Value: ""}}},
			},
		)
		if err != nil {
			return fmt.Errorf("update outbox row: %w", err)
		}
		if result.MatchedCount != 1 {
			return outbox.ErrClaimLost
		}
		if _, err := s.db.Collection(subscriptionsCollection).DeleteOne(txCtx, bson.D{
			{Key: "stick_id", Value: delivery.StickID},
			{Key: "user_sub", Value: delivery.RecipientSub},
			{Key: "generation_token", Value: delivery.SubscriptionGenerationToken},
		}); err != nil {
			return fmt.Errorf("delete subscription for stick %q: %w", delivery.StickID, err)
		}
		return nil
	})
	return wrapStoreError(fmt.Sprintf("mark notification %d succeeded", delivery.ID), err)
}

// MarkNotificationFailed records a failed delivery and its next retry time.
func (s *Store) MarkNotificationFailed(ctx context.Context, delivery outbox.Delivery, nextAttemptAt time.Time, failure string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	result, err := s.db.Collection(outboxCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: delivery.ID}, {Key: "status", Value: "in_progress"}, {Key: "claim_token", Value: delivery.ClaimToken}},
		bson.D{
			{Key: "$set", Value: bson.D{{Key: "status", Value: "failed"}, {Key: "next_attempt_at", Value: nextAttemptAt}, {Key: "last_error", Value: failure}}},
			{Key: "$unset", Value: bson.D{{Key: "claim_token", Value: ""}, {Key: "claimed_at", Value: ""}}},
		},
	)
	if err != nil {
		return fmt.Errorf("mark notification %d failed: update outbox row: %w", delivery.ID, err)
	}
	if result.MatchedCount != 1 {
		return outbox.ErrClaimLost
	}
	return nil
}
