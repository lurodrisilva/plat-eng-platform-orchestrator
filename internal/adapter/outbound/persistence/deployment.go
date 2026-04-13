package persistence

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// DeploymentRepository implements deployment.Repository using Azure DocumentDB.
type DeploymentRepository struct {
	collection *mongo.Collection
	logger     *slog.Logger
}

// NewDeploymentRepository creates a MongoDB-backed deployment repository.
func NewDeploymentRepository(collection *mongo.Collection, logger *slog.Logger) *DeploymentRepository {
	return &DeploymentRepository{collection: collection, logger: logger}
}

// deploymentDoc is the persistence representation (private to adapter).
type deploymentDoc struct {
	ID            string    `bson:"_id"`
	ApplicationID string    `bson:"applicationId"`
	Team          string    `bson:"team"`
	Status        string    `bson:"status"`
	Environment   string    `bson:"environment"`
	Cluster       string    `bson:"cluster"`
	Namespace     string    `bson:"namespace"`
	AppProject    string    `bson:"appProject"`
	ImageRepo     string    `bson:"imageRepo"`
	ImageTag      string    `bson:"imageTag"`
	ImageDigest   string    `bson:"imageDigest"`
	ChartRepo     string    `bson:"chartRepo"`
	ChartName     string    `bson:"chartName"`
	GitSHA        string    `bson:"gitSha"`
	GitRef        string    `bson:"gitRef"`
	RunID         string    `bson:"githubRunId"`
	RunAttempt    int       `bson:"githubRunAttempt"`
	Workflow      string    `bson:"workflowName"`
	Actor         string    `bson:"actor"`
	RepoFull      string    `bson:"repositoryFull"`
	CorrelationID string    `bson:"correlationId"`
	ErrMsg        string    `bson:"error,omitempty"`
	StartedAt     time.Time `bson:"startedAt"`
	CompletedAt   time.Time `bson:"completedAt,omitempty"`
	UpdatedAt     time.Time `bson:"updatedAt"`
}

func (r *DeploymentRepository) Save(ctx context.Context, d *deployment.Deployment) error {
	doc := toDoc(d)
	filter := bson.M{"_id": doc.ID}
	update := bson.M{"$set": doc}
	opts := options.Update().SetUpsert(true)

	if _, err := r.collection.UpdateOne(ctx, filter, update, opts); err != nil {
		return fmt.Errorf("save deployment %s: %w", doc.ID, err)
	}

	r.logger.DebugContext(ctx, "saved deployment",
		slog.String("id", doc.ID),
		slog.String("status", doc.Status),
	)
	return nil
}

func (r *DeploymentRepository) FindByID(ctx context.Context, id deployment.DeploymentID) (*deployment.Deployment, error) {
	var doc deploymentDoc
	filter := bson.M{"_id": id.String()}
	if err := r.collection.FindOne(ctx, filter).Decode(&doc); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, deployment.ErrNotFound
		}
		return nil, fmt.Errorf("find deployment %s: %w", id, err)
	}
	return fromDoc(doc)
}

func (r *DeploymentRepository) FindByApplication(ctx context.Context, appID, environment string, limit int) ([]*deployment.Deployment, error) {
	filter := bson.M{"applicationId": appID}
	if environment != "" {
		filter["environment"] = environment
	}
	if limit <= 0 {
		limit = 50
	}

	opts := options.Find().SetSort(bson.D{{Key: "startedAt", Value: -1}}).SetLimit(int64(limit))
	cursor, err := r.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find deployments: %w", err)
	}
	defer cursor.Close(ctx)

	var result []*deployment.Deployment
	for cursor.Next(ctx) {
		var doc deploymentDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("decode deployment: %w", err)
		}
		d, err := fromDoc(doc)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
}

func toDoc(d *deployment.Deployment) deploymentDoc {
	return deploymentDoc{
		ID:            d.ID().String(),
		ApplicationID: d.ApplicationID(),
		Team:          d.Team(),
		Status:        d.Status().String(),
		Environment:   d.Target().Environment(),
		Cluster:       d.Target().Cluster(),
		Namespace:     d.Target().Namespace(),
		AppProject:    d.Target().AppProject(),
		ImageRepo:     d.Image().Repository(),
		ImageTag:      d.Image().Tag(),
		ImageDigest:   d.Image().Digest(),
		ChartRepo:     d.ChartSource().Repository(),
		ChartName:     d.ChartSource().Name(),
		GitSHA:        d.Source().GitSHA(),
		GitRef:        d.Source().GitRef(),
		RunID:         d.Source().GitHubRunID(),
		RunAttempt:    d.Source().GitHubRunAttempt(),
		Workflow:      d.Source().WorkflowName(),
		Actor:         d.Source().Actor(),
		RepoFull:      d.Source().RepositoryFull(),
		CorrelationID: d.CorrelationID(),
		ErrMsg:        d.Error(),
		StartedAt:     d.StartedAt(),
		CompletedAt:   d.CompletedAt(),
		UpdatedAt:     d.UpdatedAt(),
	}
}

func fromDoc(doc deploymentDoc) (*deployment.Deployment, error) {
	status, err := deployment.ParseStatus(doc.Status)
	if err != nil {
		return nil, fmt.Errorf("parse status: %w", err)
	}

	image, err := deployment.NewImage(doc.ImageRepo, doc.ImageTag, doc.ImageDigest)
	if err != nil {
		return nil, fmt.Errorf("reconstitute image: %w", err)
	}

	chart, err := deployment.NewChartSource(doc.ChartRepo, doc.ChartName, "", false)
	if err != nil {
		return nil, fmt.Errorf("reconstitute chart source: %w", err)
	}

	target, err := deployment.NewTarget(doc.Environment, doc.Cluster, doc.Namespace, doc.AppProject)
	if err != nil {
		return nil, fmt.Errorf("reconstitute target: %w", err)
	}

	source, err := deployment.NewSource(doc.GitSHA, doc.GitRef, doc.RunID, doc.RunAttempt, doc.Workflow, doc.Actor, doc.RepoFull)
	if err != nil {
		return nil, fmt.Errorf("reconstitute source: %w", err)
	}

	return deployment.Reconstitute(
		deployment.DeploymentID(doc.ID),
		doc.ApplicationID, doc.Team,
		image, chart, target, source,
		nil, doc.CorrelationID,
		status,
		nil, nil, nil, nil,
		doc.ErrMsg,
		doc.StartedAt, doc.CompletedAt, doc.UpdatedAt,
	), nil
}

// Compile-time interface compliance check.
var _ deployment.Repository = (*DeploymentRepository)(nil)
