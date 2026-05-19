<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-19 | Updated: 2026-05-19 -->

# persistence

## Purpose
Implements `domain/deployment.Repository` against MongoDB / Azure DocumentDB via `go.mongodb.org/mongo-driver`. Upserts deployment docs keyed by `_id` (the `DeploymentID` string), reads by id, lists by `applicationId` (+ optional `environment`).

## Key Files
| File | Description |
|------|-------------|
| `deployment.go` | `DeploymentRepository{collection, logger}`. Private `deploymentDoc` BSON struct (no metadata/artifact/argoApp/health fields yet). `Save` upsert via `UpdateOne` with `$set` + `SetUpsert(true)`. `FindByID` decodes one doc; `mongo.ErrNoDocuments` → `domain.ErrNotFound`. `FindByApplication` filters by `applicationId` (+ env), sorts `startedAt: -1`, default limit 50. `toDoc`/`fromDoc` map aggregate ↔ BSON; `fromDoc` rebuilds value objects via their validating constructors then calls `deployment.Reconstitute(...)` with `nil` for metadata/artifact/argoApp/health |

## For AI Agents

### Working In This Directory
- **Schema gap**: `deploymentDoc` does **not** persist `Metadata`, `Artifact`, `ArgoApp`, or `Health` — `FindByID` always returns these as `nil`. Extend the BSON struct + `toDoc`/`fromDoc` before relying on these fields cross-process.
- **Schema gap**: `ChartSource.VersionConstraint` and `AllowPrerelease` are not persisted — `fromDoc` reconstructs with `("", false)`. Add fields if reproducibility from history is needed.
- `FindByApplication` returns up to `limit` docs (default 50, capped only by Mongo); add cursor pagination for unbounded scans.
- Mongo `Update` with upsert + `$set` of the **whole doc** is intentional — full replacement on every state transition. If contention becomes a problem, switch to per-field `$set` or optimistic concurrency on `updatedAt`.
- DocumentDB-on-Azure has API differences vs Mongo (e.g., limited aggregation operators); keep queries to the basic CRUD operators used here.

### Testing Requirements
- Integration test against a `mongo` testcontainer (Mongo image is the closest API to DocumentDB).

## Dependencies

### Internal
- `internal/domain/deployment`

### External
- `go.mongodb.org/mongo-driver/{mongo,bson,mongo/options}`, `time`, `log/slog`

<!-- MANUAL: -->
