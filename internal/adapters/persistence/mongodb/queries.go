package mongodb

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"stick/internal/application"
	domain "stick/internal/core"
)

// GetStick returns the stick identified by id.
func (s *Store) GetStick(ctx context.Context, id string) (domain.Stick, error) {
	if err := contextError(ctx); err != nil {
		return domain.Stick{}, err
	}
	var doc stickDocument
	err := s.db.Collection(sticksCollection).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return domain.Stick{}, application.ErrNotFound
		}
		return domain.Stick{}, fmt.Errorf("get stick %q: %w", id, err)
	}
	return stickFromDocument(doc), nil
}

// ListSticks returns all active sticks ordered by name.
func (s *Store) ListSticks(ctx context.Context) ([]domain.Stick, error) {
	return s.listSticks(ctx, bson.D{{Key: "archived_at", Value: nil}}, "list sticks")
}

// ListArchivedSticks returns all archived sticks ordered by name.
func (s *Store) ListArchivedSticks(ctx context.Context) ([]domain.Stick, error) {
	return s.listSticks(ctx, bson.D{{Key: "archived_at", Value: bson.D{{Key: "$exists", Value: true}, {Key: "$ne", Value: nil}}}}, "list archived sticks")
}

func (s *Store) listSticks(ctx context.Context, filter interface{}, operation string) ([]domain.Stick, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	cursor, err := s.db.Collection(sticksCollection).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	defer cursor.Close(ctx)
	sticks := make([]domain.Stick, 0)
	for cursor.Next(ctx) {
		var doc stickDocument
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("%s: decode stick: %w", operation, err)
		}
		sticks = append(sticks, stickFromDocument(doc))
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	return sticks, nil
}

// GetHistory returns one page of completed sessions and the total completed count.
func (s *Store) GetHistory(ctx context.Context, id string, limit, offset int) ([]domain.Session, int, error) {
	if err := contextError(ctx); err != nil {
		return nil, 0, err
	}
	if limit < 0 || offset < 0 {
		return nil, 0, fmt.Errorf("history pagination values must be non-negative")
	}

	var total int
	sessions := make([]domain.Session, 0)
	err := s.runInSnapshot(ctx, func(snapshot mongo.SessionContext) error {
		page := make([]domain.Session, 0)
		filter := bson.D{
			{Key: "stick_id", Value: id},
			{Key: "released_at", Value: bson.D{{Key: "$exists", Value: true}, {Key: "$ne", Value: nil}}},
		}
		count, err := s.db.Collection(sessionsCollection).CountDocuments(snapshot, filter)
		if err != nil {
			return fmt.Errorf("count history for stick %q: %w", id, err)
		}
		if uint64(count) > uint64(maxInt()) {
			return fmt.Errorf("history count for stick %q exceeds platform limits", id)
		}
		total = int(count)
		if limit == 0 {
			return nil
		}

		cursor, err := s.db.Collection(sessionsCollection).Find(snapshot, filter, options.Find().
			SetSort(bson.D{{Key: "claimed_at", Value: -1}, {Key: "_id", Value: -1}}).
			SetSkip(int64(offset)).SetLimit(int64(limit)))
		if err != nil {
			return fmt.Errorf("query history for stick %q: %w", id, err)
		}
		defer cursor.Close(snapshot)
		for cursor.Next(snapshot) {
			var doc sessionDocument
			if err := cursor.Decode(&doc); err != nil {
				return fmt.Errorf("scan history for stick %q: %w", id, err)
			}
			page = append(page, sessionFromDocument(doc))
		}
		if err := cursor.Err(); err != nil {
			return fmt.Errorf("iterate history for stick %q: %w", id, err)
		}
		sessions = page
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return sessions, total, nil
}
