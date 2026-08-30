package mongodb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"stick/internal/application"
	domain "stick/internal/core"
)

type transaction struct {
	store *Store
	ctx   mongo.SessionContext
}

func (t *transaction) GetStick(ctx context.Context, id string) (domain.Stick, error) {
	opCtx, err := t.operationContext(ctx)
	if err != nil {
		return domain.Stick{}, err
	}
	var doc stickDocument
	// MongoDB has no SELECT FOR UPDATE. A no-op update reserves a write slot
	// for this stick in the transaction, serializing subscription changes with
	// concurrent stick transitions just as the SQL adapters do.
	err = t.store.db.Collection(sticksCollection).FindOneAndUpdate(
		opCtx,
		bson.D{{Key: "_id", Value: id}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "version", Value: int64(0)}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return domain.Stick{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Stick{}, fmt.Errorf("get stick %q in transaction: %w", id, err)
	}
	return stickFromDocument(doc), nil
}

func (t *transaction) CreateStick(ctx context.Context, stick domain.Stick) error {
	opCtx, err := t.operationContext(ctx)
	if err != nil {
		return err
	}
	if stick.Version != 1 {
		return fmt.Errorf("create stick %q: version is %d, want 1", stick.ID, stick.Version)
	}
	_, err = t.store.db.Collection(sticksCollection).InsertOne(opCtx, stickToDocument(stick))
	if mongo.IsDuplicateKeyError(err) {
		return application.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("create stick %q: %w", stick.ID, err)
	}
	return nil
}

func (t *transaction) SaveStick(ctx context.Context, stick domain.Stick, expectedVersion int64) error {
	opCtx, err := t.operationContext(ctx)
	if err != nil {
		return err
	}
	if expectedVersion < 1 || expectedVersion == math.MaxInt64 || stick.Version != expectedVersion+1 {
		return fmt.Errorf("save stick %q: version transition %d to %d is not monotonic", stick.ID, expectedVersion, stick.Version)
	}

	doc := stickToDocument(stick)
	set := bson.M{"name": doc.Name, "version": doc.Version}
	unset := bson.M{}
	if doc.HolderSub == nil {
		for _, field := range []string{"holder_sub", "holder_name", "holder_email", "claimed_at", "reason"} {
			unset[field] = ""
		}
	} else {
		set["holder_sub"] = doc.HolderSub
		set["holder_name"] = doc.HolderName
		set["holder_email"] = doc.HolderEmail
		set["claimed_at"] = doc.ClaimedAt
		set["reason"] = doc.Reason
	}
	if doc.ArchivedAt == nil {
		unset["archived_at"] = ""
	} else {
		set["archived_at"] = doc.ArchivedAt
	}
	update := bson.M{"$set": set}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	result, err := t.store.db.Collection(sticksCollection).UpdateOne(
		opCtx,
		bson.D{{Key: "_id", Value: stick.ID}, {Key: "version", Value: expectedVersion}},
		update,
	)
	if err != nil {
		return fmt.Errorf("save stick %q at version %d: %w", stick.ID, expectedVersion, err)
	}
	if result.MatchedCount != 1 {
		return application.ErrVersionConflict
	}
	return nil
}

func (t *transaction) CreateSession(ctx context.Context, session domain.Session) error {
	opCtx, err := t.operationContext(ctx)
	if err != nil {
		return err
	}
	if err := t.ensureStickExists(opCtx, session.StickID); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	id, err := t.nextID(opCtx, sessionsCollection)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	doc := sessionDocument{
		ID:          id,
		StickID:     session.StickID,
		HolderSub:   session.HolderSub,
		HolderName:  session.HolderName,
		HolderEmail: session.HolderEmail,
		Reason:      session.Reason,
		ClaimedAt:   session.ClaimedAt,
		Active:      true,
	}
	if _, err := t.store.db.Collection(sessionsCollection).InsertOne(opCtx, doc); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (t *transaction) CloseSession(ctx context.Context, stickID, holderSub string, releasedAt time.Time) error {
	opCtx, err := t.operationContext(ctx)
	if err != nil {
		return err
	}
	result, err := t.store.db.Collection(sessionsCollection).UpdateOne(
		opCtx,
		bson.D{{Key: "stick_id", Value: stickID}, {Key: "holder_sub", Value: holderSub}, {Key: "released_at", Value: nil}},
		bson.M{"$set": bson.M{"released_at": releasedAt, "active": false}},
	)
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	if result.MatchedCount != 1 {
		return errors.New("close session: active session not found")
	}
	return nil
}

func (t *transaction) EnqueueReleaseNotifications(ctx context.Context, before domain.Stick, releasedAt time.Time) error {
	opCtx, err := t.operationContext(ctx)
	if err != nil {
		return err
	}
	if before.Holder == nil {
		return errors.New("enqueue release notifications without holder snapshot")
	}

	cursor, err := t.store.db.Collection(subscriptionsCollection).Find(opCtx, bson.D{{Key: "stick_id", Value: before.ID}})
	if err != nil {
		return fmt.Errorf("enqueue release notifications for stick %q: query subscriptions: %w", before.ID, err)
	}
	defer cursor.Close(opCtx)
	for cursor.Next(opCtx) {
		var subscription subscriptionDocument
		if err := cursor.Decode(&subscription); err != nil {
			return fmt.Errorf("enqueue release notifications for stick %q: decode subscription: %w", before.ID, err)
		}
		id, err := t.nextID(opCtx, outboxCollection)
		if err != nil {
			return fmt.Errorf("enqueue release notifications for stick %q: %w", before.ID, err)
		}
		doc := outboxDocument{
			ID:                          id,
			StickID:                     before.ID,
			StickName:                   before.Name,
			HolderName:                  before.Holder.Name,
			HolderEmail:                 before.Holder.Email,
			HeldSince:                   before.Holder.ClaimedAt,
			ReleasedAt:                  releasedAt,
			RecipientSub:                subscription.UserSub,
			RecipientName:               subscription.UserName,
			RecipientEmail:              subscription.UserEmail,
			SubscriptionGenerationToken: subscription.GenerationToken,
			Status:                      "pending",
			Attempts:                    0,
			NextAttemptAt:               &releasedAt,
			CreatedAt:                   releasedAt,
		}
		if _, err := t.store.db.Collection(outboxCollection).InsertOne(opCtx, doc); err != nil {
			return fmt.Errorf("enqueue release notifications for stick %q: %w", before.ID, err)
		}
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("enqueue release notifications for stick %q: iterate subscriptions: %w", before.ID, err)
	}
	return nil
}

func (t *transaction) Subscribe(ctx context.Context, stickID string, identity domain.Identity, generationToken string) error {
	opCtx, err := t.operationContext(ctx)
	if err != nil {
		return err
	}
	if generationToken == "" {
		return errors.New("subscribe: generation token is required")
	}
	if err := t.ensureStickExists(opCtx, stickID); err != nil {
		return fmt.Errorf("subscribe %q to stick %q: %w", identity.Sub, stickID, err)
	}
	_, err = t.store.db.Collection(subscriptionsCollection).UpdateOne(
		opCtx,
		bson.D{{Key: "stick_id", Value: stickID}, {Key: "user_sub", Value: identity.Sub}},
		bson.M{"$set": bson.M{
			"user_name": identity.Name, "user_email": identity.Email, "generation_token": generationToken,
		}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("subscribe %q to stick %q: %w", identity.Sub, stickID, err)
	}
	return nil
}

func (t *transaction) Unsubscribe(ctx context.Context, stickID, subject string) error {
	opCtx, err := t.operationContext(ctx)
	if err != nil {
		return err
	}
	if _, err := t.store.db.Collection(subscriptionsCollection).DeleteOne(
		opCtx, bson.D{{Key: "stick_id", Value: stickID}, {Key: "user_sub", Value: subject}},
	); err != nil {
		return fmt.Errorf("unsubscribe %q from stick %q: %w", subject, stickID, err)
	}
	return nil
}

func (t *transaction) nextID(ctx context.Context, counter string) (int64, error) {
	var doc counterDocument
	err := t.store.db.Collection(countersCollection).FindOneAndUpdate(
		ctx,
		bson.D{{Key: "_id", Value: counter}},
		bson.M{"$inc": bson.M{"value": int64(1)}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&doc)
	if err != nil {
		return 0, err
	}
	return doc.Value, nil
}

func (t *transaction) operationContext(ctx context.Context) (mongo.SessionContext, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if session := mongo.SessionFromContext(t.ctx); session != nil {
		return mongo.NewSessionContext(ctx, session), nil
	}
	return t.ctx, nil
}

func (t *transaction) ensureStickExists(ctx mongo.SessionContext, id string) error {
	var doc struct {
		ID string `bson:"_id"`
	}
	err := t.store.db.Collection(sticksCollection).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return application.ErrNotFound
	}
	return err
}
