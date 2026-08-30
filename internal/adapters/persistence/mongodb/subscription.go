package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SubscribedStickIDs returns active sticks subscribed to by subject.
func (s *Store) SubscribedStickIDs(ctx context.Context, subject string) ([]string, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}

	subscriptions, err := s.db.Collection(subscriptionsCollection).Find(ctx,
		bson.D{{Key: "user_sub", Value: subject}},
		options.Find().SetProjection(bson.D{{Key: "stick_id", Value: 1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions for %q: %w", subject, err)
	}
	defer subscriptions.Close(ctx)

	ids := make([]string, 0)
	for subscriptions.Next(ctx) {
		var subscription subscriptionDocument
		if err := subscriptions.Decode(&subscription); err != nil {
			return nil, fmt.Errorf("scan subscription for %q: %w", subject, err)
		}
		ids = append(ids, subscription.StickID)
	}
	if err := subscriptions.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions for %q: %w", subject, err)
	}
	if len(ids) == 0 {
		return ids, nil
	}

	// MongoDB does not have relational joins in ordinary finds. Resolve the
	// subscribed IDs against active sticks and sort the result to match the SQL
	// adapters' stable contract.
	cursor, err := s.db.Collection(sticksCollection).Find(ctx,
		bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}, {Key: "archived_at", Value: nil}},
		options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}).SetSort(bson.D{{Key: "_id", Value: 1}}),
	)
	if err != nil {
		return nil, fmt.Errorf("query active subscribed sticks for %q: %w", subject, err)
	}
	defer cursor.Close(ctx)
	activeIDs := make([]string, 0, len(ids))
	for cursor.Next(ctx) {
		var doc struct {
			ID string `bson:"_id"`
		}
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("scan active subscription for %q: %w", subject, err)
		}
		activeIDs = append(activeIDs, doc.ID)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate active subscriptions for %q: %w", subject, err)
	}
	return activeIDs, nil
}
