package persistence

import (
	"strings"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

func testAggregate(t *testing.T, appID string, resources []deployment.Resource) *deployment.Deployment {
	t.Helper()
	image, err := deployment.NewImage("ghcr.io/acme/"+appID, "latest", "sha256:"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	chart, err := deployment.NewChartSource("ghcr.io/acme/helm-charts", appID+"-umbrella", "*", true)
	if err != nil {
		t.Fatalf("chart: %v", err)
	}
	target, err := deployment.NewTarget("development", "aks-test", appID+"-development", "platform")
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	source, err := deployment.NewSource("abcdef1234567", "refs/heads/main", "", 0, "", "", "acme/"+appID)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	d, err := deployment.New(appID, "payments", image, chart, target, source, nil, resources, "corr-1")
	if err != nil {
		t.Fatalf("new deployment: %v", err)
	}
	return d
}

// The gap that would make the whole feature a no-op in production. The executor
// reloads the deployment from the repository before composing — including on the
// FIRST drive, not only after a restart — so a resource absent from the document
// is a chart composed without the database on every deploy, while every other
// signal stays green.
func TestDeploymentDoc_RoundTripsDeclaredResources(t *testing.T) {
	pg, err := deployment.NewPostgresResource("orders-v3", "medium", "15", 65536)
	if err != nil {
		t.Fatalf("resource: %v", err)
	}
	d := testAggregate(t, "orders-v3", []deployment.Resource{pg})

	back, err := fromDoc(toDoc(d))
	if err != nil {
		t.Fatalf("fromDoc: %v", err)
	}

	got := back.Resources()
	if len(got) != 1 {
		t.Fatalf("round-tripped %d resources, want 1", len(got))
	}
	if got[0].Type() != deployment.ResourceTypePostgres ||
		got[0].Size() != "medium" || got[0].Version() != "15" || got[0].StorageMb() != 65536 {
		t.Errorf("shape = %s/%s/%s/%d, want postgres/medium/15/65536",
			got[0].Type(), got[0].Size(), got[0].Version(), got[0].StorageMb())
	}
}

// The name is not stored; it is re-derived from the application id on read, so
// the identity that ties the XR, the Azure server and the app's bind together
// cannot drift in the database either.
func TestDeploymentDoc_ResourceNameIsRederivedNotStored(t *testing.T) {
	pg, err := deployment.NewPostgresResource("orders-v3", "small", "16", 32768)
	if err != nil {
		t.Fatalf("resource: %v", err)
	}
	doc := toDoc(testAggregate(t, "orders-v3", []deployment.Resource{pg}))

	// Nothing in the persisted resource carries a name...
	if len(doc.Resources) != 1 {
		t.Fatalf("persisted %d resources, want 1", len(doc.Resources))
	}
	if doc.Resources[0].Type != "postgres" {
		t.Errorf("persisted type = %q, want postgres", doc.Resources[0].Type)
	}

	// ...and the name comes back anyway, from the application id.
	back, err := fromDoc(doc)
	if err != nil {
		t.Fatalf("fromDoc: %v", err)
	}
	if got := back.Resources()[0]; got.Name() != "orders-v3" || got.DatabaseName() != "orders-v3-db" {
		t.Errorf("names = %s/%s, want orders-v3/orders-v3-db", got.Name(), got.DatabaseName())
	}
}

// Every deployment created before declared dependencies existed has no
// `resources` field at all. It must round-trip as nil rather than as an empty
// set that changes what gets composed.
func TestDeploymentDoc_NoResourcesRoundTripsAsNil(t *testing.T) {
	back, err := fromDoc(toDoc(testAggregate(t, "orders-v3", nil)))
	if err != nil {
		t.Fatalf("fromDoc: %v", err)
	}
	if back.Resources() != nil {
		t.Errorf("Resources() = %#v, want nil", back.Resources())
	}
}

// A stored resource that no longer validates is an error, not a silent drop.
// Dropping it would produce a deployment that reads as healthy while composing
// a chart without the database it was created with.
func TestDeploymentDoc_UnrebuildableResourceIsAnError(t *testing.T) {
	doc := toDoc(testAggregate(t, "orders-v3", nil))
	doc.Resources = []resourceDoc{{Type: "postgres", Size: "enormous", Version: "16", StorageMb: 32768}}

	if _, err := fromDoc(doc); err == nil {
		t.Fatal("a resource that cannot be rebuilt must be an error")
	}
}
