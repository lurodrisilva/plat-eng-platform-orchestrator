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
	ID              string         `bson:"_id"`
	ApplicationID   string         `bson:"applicationId"`
	Team            string         `bson:"team"`
	Status          string         `bson:"status"`
	Environment     string         `bson:"environment"`
	Cluster         string         `bson:"cluster"`
	Namespace       string         `bson:"namespace"`
	AppProject      string         `bson:"appProject"`
	ImageRepo       string         `bson:"imageRepo"`
	ImageTag        string         `bson:"imageTag"`
	ImageDigest     string         `bson:"imageDigest"`
	ChartRepo       string         `bson:"chartRepo"`
	ChartName       string         `bson:"chartName"`
	ChartConstraint string         `bson:"chartConstraint,omitempty"`
	ChartAllowPre   bool           `bson:"chartAllowPrerelease,omitempty"`
	Values          map[string]any `bson:"values,omitempty"`
	GitSHA          string         `bson:"gitSha"`
	GitRef          string         `bson:"gitRef"`
	RunID           string         `bson:"githubRunId"`
	RunAttempt      int            `bson:"githubRunAttempt"`
	Workflow        string         `bson:"workflowName"`
	Actor           string         `bson:"actor"`
	RepoFull        string         `bson:"repositoryFull"`
	CorrelationID   string         `bson:"correlationId"`
	ErrMsg          string         `bson:"error,omitempty"`
	StartedAt       time.Time      `bson:"startedAt"`
	CompletedAt     time.Time      `bson:"completedAt,omitempty"`
	UpdatedAt       time.Time      `bson:"updatedAt"`

	// Pipeline outputs — populated as the deployment advances (ADR-0016).
	Metadata *metadataDoc `bson:"metadata,omitempty"`
	Artifact *artifactDoc `bson:"artifact,omitempty"`
	ArgoApp  *argoAppDoc  `bson:"argoApp,omitempty"`
	Health   *healthDoc   `bson:"health,omitempty"`
}

type metadataDoc struct {
	ComponentID       string            `bson:"componentId"`
	DeploymentVersion string            `bson:"deploymentVersion"`
	ArgoAppName       string            `bson:"argoAppName"`
	Labels            map[string]string `bson:"labels,omitempty"`
}

type artifactDoc struct {
	OCIReference string `bson:"ociReference"`
	Digest       string `bson:"digest"`
	Registry     string `bson:"registry"`
	Repository   string `bson:"repository"`
	Tag          string `bson:"tag"`
}

type argoAppDoc struct {
	Name      string `bson:"name"`
	Namespace string `bson:"namespace"`
	Project   string `bson:"project"`
}

type healthDoc struct {
	SyncStatus   string    `bson:"syncStatus"`
	HealthStatus string    `bson:"healthStatus"`
	Message      string    `bson:"message,omitempty"`
	EvaluatedAt  time.Time `bson:"evaluatedAt"`
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
	defer func() { _ = cursor.Close(ctx) }()

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
	doc := deploymentDoc{
		ID:              d.ID().String(),
		ApplicationID:   d.ApplicationID(),
		Team:            d.Team(),
		Status:          d.Status().String(),
		Environment:     d.Target().Environment(),
		Cluster:         d.Target().Cluster(),
		Namespace:       d.Target().Namespace(),
		AppProject:      d.Target().AppProject(),
		ImageRepo:       d.Image().Repository(),
		ImageTag:        d.Image().Tag(),
		ImageDigest:     d.Image().Digest(),
		ChartRepo:       d.ChartSource().Repository(),
		ChartName:       d.ChartSource().Name(),
		ChartConstraint: d.ChartSource().VersionConstraint(),
		ChartAllowPre:   d.ChartSource().AllowPrerelease(),
		Values:          d.Values(),
		GitSHA:          d.Source().GitSHA(),
		GitRef:          d.Source().GitRef(),
		RunID:           d.Source().GitHubRunID(),
		RunAttempt:      d.Source().GitHubRunAttempt(),
		Workflow:        d.Source().WorkflowName(),
		Actor:           d.Source().Actor(),
		RepoFull:        d.Source().RepositoryFull(),
		CorrelationID:   d.CorrelationID(),
		ErrMsg:          d.Error(),
		StartedAt:       d.StartedAt(),
		CompletedAt:     d.CompletedAt(),
		UpdatedAt:       d.UpdatedAt(),
	}

	if m := d.Metadata(); m != nil {
		doc.Metadata = &metadataDoc{
			ComponentID:       m.ComponentID.String(),
			DeploymentVersion: m.DeploymentVersion.String(),
			ArgoAppName:       m.ArgoAppName.String(),
			Labels:            m.Labels,
		}
	}
	if a := d.ArtifactInfo(); a != nil {
		doc.Artifact = &artifactDoc{
			OCIReference: a.OCIReference, Digest: a.Digest, Registry: a.Registry,
			Repository: a.Repository, Tag: a.Tag,
		}
	}
	if g := d.ArgoApp(); g != nil {
		doc.ArgoApp = &argoAppDoc{Name: g.Name, Namespace: g.Namespace, Project: g.Project}
	}
	if hl := d.HealthInfo(); hl != nil {
		doc.Health = &healthDoc{
			SyncStatus: hl.SyncStatus, HealthStatus: hl.HealthStatus,
			Message: hl.Message, EvaluatedAt: hl.EvaluatedAt,
		}
	}
	return doc
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

	chart, err := deployment.NewChartSource(doc.ChartRepo, doc.ChartName, doc.ChartConstraint, doc.ChartAllowPre)
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

	var metadata *deployment.Metadata
	if m := doc.Metadata; m != nil {
		metadata = &deployment.Metadata{
			ComponentID:       deployment.ComponentID(m.ComponentID),
			DeploymentVersion: deployment.DeploymentVersion(m.DeploymentVersion),
			ArgoAppName:       deployment.ArgoAppName(m.ArgoAppName),
			Labels:            m.Labels,
		}
	}
	var artifact *deployment.Artifact
	if a := doc.Artifact; a != nil {
		artifact = &deployment.Artifact{
			OCIReference: a.OCIReference, Digest: a.Digest, Registry: a.Registry,
			Repository: a.Repository, Tag: a.Tag,
		}
	}
	var argoApp *deployment.ArgoApp
	if g := doc.ArgoApp; g != nil {
		argoApp = &deployment.ArgoApp{Name: g.Name, Namespace: g.Namespace, Project: g.Project}
	}
	var health *deployment.Health
	if hl := doc.Health; hl != nil {
		health = &deployment.Health{
			SyncStatus: hl.SyncStatus, HealthStatus: hl.HealthStatus,
			Message: hl.Message, EvaluatedAt: hl.EvaluatedAt,
		}
	}

	return deployment.Reconstitute(
		deployment.DeploymentID(doc.ID),
		doc.ApplicationID, doc.Team,
		image, chart, target, source,
		doc.Values, doc.CorrelationID,
		status,
		metadata, artifact, argoApp, health,
		doc.ErrMsg,
		doc.StartedAt, doc.CompletedAt, doc.UpdatedAt,
	), nil
}

// Compile-time interface compliance check.
var _ deployment.Repository = (*DeploymentRepository)(nil)
