package documentdb

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/myorg/platform-orchestrator/internal/config"
	"github.com/myorg/platform-orchestrator/internal/state"
)

// MongoRepository implements state.Repository using Azure DocumentDB (Cosmos DB Mongo API).
type MongoRepository struct {
	deployments *mongo.Collection
	events      *mongo.Collection
	logger      *slog.Logger
}

// NewRepository creates a MongoDB-backed state repository.
func NewRepository(ctx context.Context, cfg config.DocumentDBConfig, logger *slog.Logger) (*MongoRepository, error) {
	clientOpts := options.Client().ApplyURI(cfg.ConnectionString)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("connecting to DocumentDB: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("pinging DocumentDB: %w", err)
	}

	db := client.Database(cfg.Database)
	return &MongoRepository{
		deployments: db.Collection(cfg.DeploymentsCollection),
		events:      db.Collection(cfg.EventsCollection),
		logger:      logger,
	}, nil
}

func (r *MongoRepository) UpsertDeployment(ctx context.Context, doc *state.DeploymentDocument) error {
	doc.UpdatedAt = time.Now().UTC()
	filter := bson.M{"_id": doc.ID}
	update := bson.M{"$set": doc}
	opts := options.Update().SetUpsert(true)

	_, err := r.deployments.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return fmt.Errorf("upserting deployment %s: %w", doc.ID, err)
	}

	r.logger.DebugContext(ctx, "upserted deployment",
		slog.String("id", doc.ID),
		slog.String("status", string(doc.Status)),
	)
	return nil
}

func (r *MongoRepository) GetDeployment(ctx context.Context, deploymentID string) (*state.DeploymentDocument, error) {
	filter := bson.M{"_id": deploymentID}
	var doc state.DeploymentDocument
	if err := r.deployments.FindOne(ctx, filter).Decode(&doc); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("deployment %s not found", deploymentID)
		}
		return nil, fmt.Errorf("finding deployment %s: %w", deploymentID, err)
	}
	return &doc, nil
}

func (r *MongoRepository) ListDeployments(ctx context.Context, filter state.DeploymentFilter) ([]state.DeploymentDocument, error) {
	query := bson.M{}
	if filter.ApplicationID != "" {
		query["applicationId"] = filter.ApplicationID
	}
	if filter.Environment != "" {
		query["environment"] = filter.Environment
	}
	if filter.Status != "" {
		query["status"] = filter.Status
	}
	if filter.Actor != "" {
		query["source.actor"] = filter.Actor
	}
	if filter.Since != nil {
		query["startedAt"] = bson.M{"$gte": filter.Since}
	}

	limit := int64(50)
	if filter.Limit > 0 {
		limit = int64(filter.Limit)
	}

	opts := options.Find().
		SetSort(bson.D{{Key: "startedAt", Value: -1}}).
		SetLimit(limit)

	cursor, err := r.deployments.Find(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []state.DeploymentDocument
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decoding deployments: %w", err)
	}
	return docs, nil
}

func (r *MongoRepository) AppendEvent(ctx context.Context, event *state.EventDocument) error {
	_, err := r.events.InsertOne(ctx, event)
	if err != nil {
		return fmt.Errorf("inserting event: %w", err)
	}
	r.logger.DebugContext(ctx, "appended event",
		slog.String("deploymentId", event.DeploymentID),
		slog.String("state", string(event.State)),
	)
	return nil
}

func (r *MongoRepository) ListEvents(ctx context.Context, deploymentID string) ([]state.EventDocument, error) {
	filter := bson.M{"deploymentId": deploymentID}
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}})

	cursor, err := r.events.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("listing events for %s: %w", deploymentID, err)
	}
	defer cursor.Close(ctx)

	var events []state.EventDocument
	if err := cursor.All(ctx, &events); err != nil {
		return nil, fmt.Errorf("decoding events: %w", err)
	}
	return events, nil
}

// Ensure MongoRepository implements Repository at compile time.
var _ state.Repository = (*MongoRepository)(nil)

