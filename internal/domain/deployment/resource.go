package deployment

import (
	"fmt"
	"regexp"
	"strings"
)

// ResourceType enumerates the application dependencies the platform can
// provision. It is a list rather than a boolean flag so a second type (cache,
// object store) is additive — see ADR-0023.
//
// Only postgres is implemented. Cache is deliberately absent: both legacy Azure
// Redis products are hard-retired and Azure Managed Redis will not provision in
// the target subscription (ADR-0019).
type ResourceType string

// ResourceTypePostgres is a PostgreSQL database. Under the paved road it is an
// Azure Database for PostgreSQL Flexible Server, provisioned by the control
// plane from a PostgresInstance XR (ADR-0020).
const ResourceTypePostgres ResourceType = "postgres"

func (t ResourceType) String() string { return string(t) }

// Postgres defaults, chosen to be the cheapest structurally-valid request the
// XRD accepts. size has no burstable tier — small already maps to a General
// Purpose SKU — so these bound storage and version, not unit price (ADR-0023).
const (
	DefaultPostgresSize      = "small"
	DefaultPostgresVersion   = "16"
	DefaultPostgresStorageMb = 32768
)

// The structural contract of the PostgresInstance XRD
// (plat-eng-control-plane/chart/templates/20-xrd-postgresinstance.yaml).
// Violating it renders a manifest the API server rejects at apply time, long
// after the deployment was accepted — so it is checked here, at the boundary.
var (
	postgresSizes    = []string{"small", "medium", "large"}
	postgresVersions = []string{"14", "15", "16"}
)

const (
	minPostgresStorageMb = 32768
	maxPostgresStorageMb = 33553408
)

// resourceNameRE is the RFC 1123 label form Kubernetes requires for the XR name
// and Azure requires for the server name.
var resourceNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// maxResourceNameLen bounds the DERIVED name, and the bound comes from Azure,
// not Kubernetes. A Flexible Server name is at most 63 characters, and the
// composed managed resource appends a 13-character uniquifier ("-" + 12 hex,
// e.g. acct-6584520516bb), leaving 50. The scaffolder applies the same 50-char
// guard for the same reason; the two must not drift.
const maxResourceNameLen = 50

// Resource is a declared application dependency: billed infrastructure the
// platform provisions alongside the app, as part of the same deploy unit.
//
// The name is DERIVED from the application id and is never accepted from a
// caller (ADR-0023). That is the whole point of the type: the XR, the Azure
// server, and the app's bind are all computed from one input, so there is no
// way for them to disagree. The reported symptom that motivated this slice was
// exactly that disagreement — an app running against a server named after the
// template it was scaffolded from.
type Resource struct {
	resourceType ResourceType
	name         string
	databaseName string
	size         string
	version      string
	storageMb    int
}

// NewPostgresResource builds a validated postgres dependency for an
// application. Empty size/version/storageMb take the defaults; anything the
// XRD would reject is an error here rather than at apply time.
//
// applicationID is the ONLY identity input. name and databaseName are derived
// from it, so a caller cannot name the database independently of the app.
func NewPostgresResource(applicationID, size, version string, storageMb int) (Resource, error) {
	name, err := deriveResourceName(applicationID)
	if err != nil {
		return Resource{}, err
	}

	if size = strings.TrimSpace(size); size == "" {
		size = DefaultPostgresSize
	}
	if !contains(postgresSizes, size) {
		return Resource{}, fmt.Errorf("postgres size %q is not one of %s", size, strings.Join(postgresSizes, "|"))
	}

	if version = strings.TrimSpace(version); version == "" {
		version = DefaultPostgresVersion
	}
	if !contains(postgresVersions, version) {
		return Resource{}, fmt.Errorf("postgres version %q is not one of %s", version, strings.Join(postgresVersions, "|"))
	}

	if storageMb == 0 {
		storageMb = DefaultPostgresStorageMb
	}
	if storageMb < minPostgresStorageMb || storageMb > maxPostgresStorageMb {
		return Resource{}, fmt.Errorf("postgres storageMb %d is outside the supported range %d-%d",
			storageMb, minPostgresStorageMb, maxPostgresStorageMb)
	}

	return Resource{
		resourceType: ResourceTypePostgres,
		name:         name,
		// The building block creates this database on the server. Hyphens are
		// fine — the provider quotes the identifier.
		databaseName: name + "-db",
		size:         size,
		version:      version,
		storageMb:    storageMb,
	}, nil
}

// NewResource builds a resource of the requested type. An unimplemented type is
// an error, so a caller asking for something the platform cannot provision gets
// a refusal rather than a deployment that silently omits it.
func NewResource(resourceType, applicationID, size, version string, storageMb int) (Resource, error) {
	switch ResourceType(strings.TrimSpace(resourceType)) {
	case ResourceTypePostgres:
		return NewPostgresResource(applicationID, size, version, storageMb)
	case "":
		return Resource{}, fmt.Errorf("resource type is required")
	default:
		return Resource{}, fmt.Errorf("resource type %q is not supported (implemented: %s)",
			resourceType, ResourceTypePostgres)
	}
}

// deriveResourceName computes the dependency name from the application id and
// checks it against the constraints the XR name, the Azure server name, and the
// composed Secret names all inherit from it.
func deriveResourceName(applicationID string) (string, error) {
	name := strings.TrimSpace(applicationID)
	if name == "" {
		return "", fmt.Errorf("application ID is required to derive a resource name")
	}
	if len(name) > maxResourceNameLen {
		return "", fmt.Errorf("application ID %q is %d characters; a resource name derived from it must be at most %d "+
			"(an Azure Flexible Server name is 63 and the composed managed resource adds a 13-character uniquifier)",
			name, len(name), maxResourceNameLen)
	}
	if !resourceNameRE.MatchString(name) {
		return "", fmt.Errorf("application ID %q cannot name a resource: must be lowercase alphanumeric or '-', "+
			"starting and ending alphanumeric (RFC 1123 label)", name)
	}
	return name, nil
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// --- Getters ---

func (r Resource) Type() ResourceType { return r.resourceType }

// Name is the derived dependency name. It names the PostgresInstance XR, and
// the control plane composes <name>-postgres-conn and <name>-admin-password off
// it, which is what the application binds to.
func (r Resource) Name() string { return r.name }

// DatabaseName is the database created on the server.
func (r Resource) DatabaseName() string { return r.databaseName }

func (r Resource) Size() string    { return r.size }
func (r Resource) Version() string { return r.version }
func (r Resource) StorageMb() int  { return r.storageMb }

// IsZero reports whether the resource is the empty value.
func (r Resource) IsZero() bool { return r.resourceType == "" }
