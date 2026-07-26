package deploymentapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/myorg/platform-orchestrator/internal/application/port"
	"github.com/myorg/platform-orchestrator/internal/domain/deployment"
)

// --- fakes -------------------------------------------------------------------

// allowPolicy allows everything, so a test can isolate the translator from
// governance.
type allowPolicy struct{}

func (allowPolicy) Evaluate(context.Context, []port.ResourceRequest, string) port.ResourceDecision {
	return port.ResourceDecision{Mode: "enforce"}
}

// denyPolicy refuses everything with a fixed reason.
type denyPolicy struct{ reason string }

func (p denyPolicy) Evaluate(context.Context, []port.ResourceRequest, string) port.ResourceDecision {
	return port.ResourceDecision{Reject: true, Mode: "enforce", Violations: []string{p.reason}}
}

// recordingPolicy captures what the handler asked it to judge.
type recordingPolicy struct {
	gotResources   []port.ResourceRequest
	gotEnvironment string
}

func (p *recordingPolicy) Evaluate(_ context.Context, rs []port.ResourceRequest, env string) port.ResourceDecision {
	p.gotResources = rs
	p.gotEnvironment = env
	return port.ResourceDecision{Mode: "enforce"}
}

func testDeployment(t *testing.T, appID, team, environment string, resources []deployment.Resource) *deployment.Deployment {
	t.Helper()
	image, err := deployment.NewImage("ghcr.io/acme/"+appID, "latest", "sha256:"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	chart, err := deployment.NewChartSource("ghcr.io/acme/helm-charts", appID+"-umbrella", "*", false)
	if err != nil {
		t.Fatalf("chart: %v", err)
	}
	target, err := deployment.NewTarget(environment, "aks-test", appID+"-"+environment, "platform")
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	source, err := deployment.NewSource("abcdef1234567", "refs/heads/main", "", 0, "", "", "acme/"+appID)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	d, err := deployment.New(appID, team, image, chart, target, source, nil, resources, "corr-1")
	if err != nil {
		t.Fatalf("new deployment: %v", err)
	}
	return d
}

func mustPostgres(t *testing.T, appID, size, version string, storageMb int) deployment.Resource {
	t.Helper()
	r, err := deployment.NewPostgresResource(appID, size, version, storageMb)
	if err != nil {
		t.Fatalf("new postgres resource: %v", err)
	}
	return r
}

// --- the translator ----------------------------------------------------------

// appKey is a SCAFFOLDED app's subchart key. Deliberately not `hex-scaffold`:
// that is the template's name, and the scaffolder substitutes it for the app's
// own when it renders, so every test asserting the literal was asserting the one
// case that does not occur in production.
const appKey = "orders-v3"

// The bind must land under the app's OWN subchart key and nowhere else.
//
// Proven against a real scaffolded render before being fixed here: `helm
// template --set hex-scaffold.postgres.bindBuildingBlock.instanceName=X` left
// the rendered Secret refs on the repository's value, because Helm discards
// values addressed to a subchart that does not exist — silently. Meanwhile the
// XR name DID change, since `sqldatabase` is a real alias that survives the
// rename. So the platform renamed the database and failed to move the app onto
// it, and nothing anywhere reported a problem.
func TestPlatformValues_BindLandsUnderTheAppsOwnKeyOnly(t *testing.T) {
	d := testDeployment(t, appKey, "payments", "development",
		[]deployment.Resource{mustPostgres(t, appKey, "small", "16", 32768)})

	vals, err := platformValues(d, appKey)
	if err != nil {
		t.Fatalf("platformValues: %v", err)
	}
	if _, wrong := vals["hex-scaffold"]; wrong {
		t.Errorf("bind written under the template's key; Helm would discard it: %#v", vals)
	}
	bind, ok := vals[appKey].(map[string]any)
	if !ok {
		t.Fatalf("no values under the app's subchart key %q: %#v", appKey, vals)
	}
	got := bind["postgres"].(map[string]any)["bindBuildingBlock"].(map[string]any)["instanceName"]
	if got != appKey {
		t.Errorf("instanceName = %v, want %q", got, appKey)
	}
}

// With resources declared and no key to address, composing would provision a
// database the app is not bound to. Refuse instead: this is the same fail-closed
// rule the resource policy follows, for the same reason — the alternative costs
// money and looks green.
func TestPlatformValues_UnknownAppKeyWithResourcesRefuses(t *testing.T) {
	d := testDeployment(t, appKey, "payments", "development",
		[]deployment.Resource{mustPostgres(t, appKey, "small", "16", 32768)})

	vals, err := platformValues(d, "")
	if err == nil {
		t.Fatalf("platformValues succeeded with no subchart key: %#v", vals)
	}
	if vals != nil {
		t.Errorf("values returned alongside an error: %#v", vals)
	}
}

// ...but a deployment declaring nothing is unaffected. The gate costs nothing
// until someone asks the platform to spend money, so charts whose shape we
// cannot read still deploy as long as they ask for no dependencies.
func TestPlatformValues_UnknownAppKeyWithoutResourcesIsFine(t *testing.T) {
	d := testDeployment(t, appKey, "payments", "development", nil)
	vals, err := platformValues(d, "")
	if err != nil {
		t.Fatalf("platformValues errored for a deployment with no resources: %v", err)
	}
	if vals != nil {
		t.Errorf("platformValues = %#v, want nil", vals)
	}
}

// A deployment declaring no resources must be byte-for-byte what it was before
// this feature existed. nil, not an empty map: an empty map would still merge
// and could shift the composed values.yaml for every deployment that predates
// declared dependencies.
func TestPlatformValues_NoResourcesEmitsNil(t *testing.T) {
	got, err := platformValues(testDeployment(t, "orders-v3", "payments", "development", nil), "orders-v3")
	if err != nil {
		t.Fatalf("platformValues: %v", err)
	}
	if got != nil {
		t.Errorf("platformValues = %#v, want nil", got)
	}
}

// The exact chart shape. This is the contract between the orchestrator and the
// umbrella's values.yaml — every key here is one the chart reads, and a drift in
// either direction is an app deployed without its database.
func TestPlatformValues_OnePostgresEmitsTheExactChartShape(t *testing.T) {
	d := testDeployment(t, "orders-v3", "payments", "development",
		[]deployment.Resource{mustPostgres(t, "orders-v3", "medium", "16", 65536)})

	want := map[string]any{
		"sqldatabase": map[string]any{
			"enabled":     true,
			"engine":      "azure-flexibleserver",
			"team":        "payments",
			"environment": "development",
			"databases": map[string]any{
				"sql": []any{
					map[string]any{
						"name": "orders-v3",
						"azureFlexibleServer": map[string]any{
							"size":         "medium",
							"version":      "16",
							"storageMb":    65536,
							"databaseName": "orders-v3-db",
						},
					},
				},
			},
		},
		appKey: map[string]any{
			"postgres": map[string]any{
				"bindBuildingBlock": map[string]any{
					"enabled":      true,
					"instanceName": "orders-v3",
				},
			},
		},
	}

	got, err := platformValues(d, appKey)
	if err != nil {
		t.Fatalf("platformValues: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("platformValues mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

// The failure this whole slice exists to close: an app bound to a database that
// is not the one the deployment provisions. Both halves come from one Resource,
// so they cannot disagree — assert that directly rather than trusting it.
func TestPlatformValues_XRNameAndBindAreTheSameValue(t *testing.T) {
	d := testDeployment(t, "orders-v3", "payments", "development",
		[]deployment.Resource{mustPostgres(t, "orders-v3", "", "", 0)})
	vals, err := platformValues(d, appKey)
	if err != nil {
		t.Fatalf("platformValues: %v", err)
	}

	sql := vals["sqldatabase"].(map[string]any)["databases"].(map[string]any)["sql"].([]any)
	xrName := sql[0].(map[string]any)["name"]
	bind := vals[appKey].(map[string]any)["postgres"].(map[string]any)["bindBuildingBlock"].(map[string]any)

	if xrName != bind["instanceName"] {
		t.Fatalf("XR name %v != bind instanceName %v", xrName, bind["instanceName"])
	}
	if xrName != "orders-v3" {
		t.Errorf("name = %v, want the application id", xrName)
	}
}

// Defaults must survive the translator, not be silently re-defaulted to
// something else on the way to the chart.
func TestPlatformValues_DefaultsReachTheChart(t *testing.T) {
	d := testDeployment(t, "orders", "payments", "development",
		[]deployment.Resource{mustPostgres(t, "orders", "", "", 0)})
	pv, err := platformValues(d, "orders")
	if err != nil {
		t.Fatalf("platformValues: %v", err)
	}
	entry := pv["sqldatabase"].(map[string]any)["databases"].(map[string]any)["sql"].([]any)[0].(map[string]any)
	azure := entry["azureFlexibleServer"].(map[string]any)

	if azure["size"] != deployment.DefaultPostgresSize ||
		azure["version"] != deployment.DefaultPostgresVersion ||
		azure["storageMb"] != deployment.DefaultPostgresStorageMb {
		t.Errorf("defaults = %v, want %s/%s/%d", azure,
			deployment.DefaultPostgresSize, deployment.DefaultPostgresVersion, deployment.DefaultPostgresStorageMb)
	}
}

// --- the create boundary -----------------------------------------------------

func TestBuildResources_NamesEveryResourceAfterTheApplication(t *testing.T) {
	resources, err := BuildResources("orders-v3", []ResourceSpec{{Type: "postgres", Size: "small", Version: "16"}})
	if err != nil {
		t.Fatalf("BuildResources: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("got %d resources, want 1", len(resources))
	}
	if resources[0].Name() != "orders-v3" || resources[0].DatabaseName() != "orders-v3-db" {
		t.Errorf("names = %s/%s, want orders-v3/orders-v3-db", resources[0].Name(), resources[0].DatabaseName())
	}
}

// A structurally invalid request is a bad request, not a policy refusal — the
// caller can fix the first by editing the body and the second only by asking
// for less.
func TestBuildResources_ReportsTheOffendingIndex(t *testing.T) {
	_, err := BuildResources("orders", []ResourceSpec{
		{Type: "postgres", Size: "small"},
		{Type: "postgres", Size: "enormous"},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "resources[1]") {
		t.Errorf("error = %q, want it to name resources[1]", err.Error())
	}
}

// A second postgres derives the SAME name, so it is not a second database — it
// is a collision, and the app's single bind can only reach one of them. Refused
// at the aggregate, independently of whatever ceiling policy configures.
func TestNewDeployment_RefusesASecondPostgres(t *testing.T) {
	image, _ := deployment.NewImage("ghcr.io/acme/orders", "latest", "sha256:"+strings.Repeat("a", 64))
	chart, _ := deployment.NewChartSource("ghcr.io/acme/helm-charts", "orders-umbrella", "*", false)
	target, _ := deployment.NewTarget("development", "aks-test", "orders-development", "platform")
	source, _ := deployment.NewSource("abcdef1234567", "", "", 0, "", "", "")

	_, err := deployment.New("orders", "payments", image, chart, target, source, nil,
		[]deployment.Resource{mustPostgres(t, "orders", "", "", 0), mustPostgres(t, "orders", "", "", 0)}, "")
	if err == nil {
		t.Fatal("a second postgres must be refused")
	}
	if !strings.Contains(err.Error(), "more than one postgres") {
		t.Errorf("error = %q, want it to name the collision", err.Error())
	}
}

func TestAuthorizeResources_RefusesWhenPolicyRefuses(t *testing.T) {
	err := AuthorizeResources(context.Background(), denyPolicy{"postgres is not allowed in production"},
		[]deployment.Resource{mustPostgres(t, "orders", "", "", 0)}, "production", nil)

	var notAllowed *ResourceNotAllowedError
	if !errors.As(err, &notAllowed) {
		t.Fatalf("err = %v, want a ResourceNotAllowedError", err)
	}
	if !errors.Is(err, deployment.ErrResourceNotAllowed) {
		t.Error("the error must unwrap to the domain sentinel so callers can match it")
	}
	if !strings.Contains(err.Error(), "not allowed in production") {
		t.Errorf("error = %q, want it to carry the policy's reason", err.Error())
	}
}

// Everywhere else a nil collaborator means "gate not configured, carry on".
// Here carrying on spends money, so the absent gate must refuse instead.
func TestAuthorizeResources_NilEvaluatorRefusesRatherThanSkips(t *testing.T) {
	err := AuthorizeResources(context.Background(), nil,
		[]deployment.Resource{mustPostgres(t, "orders", "", "", 0)}, "development", nil)
	if err == nil {
		t.Fatal("a nil resource policy must refuse a declared resource, not permit it")
	}
	// ...but only when there is something to authorize.
	if err := AuthorizeResources(context.Background(), nil, nil, "development", nil); err != nil {
		t.Errorf("a deployment declaring no resources must be unaffected: %v", err)
	}
}

// Policy judges what may be provisioned, not what it is called. The derived
// name must not leak into the decision, or a rename becomes a policy change.
func TestAuthorizeResources_PassesTheShapeNotTheName(t *testing.T) {
	rec := &recordingPolicy{}
	if err := AuthorizeResources(context.Background(), rec,
		[]deployment.Resource{mustPostgres(t, "orders-v3", "medium", "15", 65536)}, "development", nil); err != nil {
		t.Fatalf("AuthorizeResources: %v", err)
	}
	want := []port.ResourceRequest{{Type: "postgres", Size: "medium", Version: "15", StorageMb: 65536}}
	if !reflect.DeepEqual(rec.gotResources, want) {
		t.Errorf("policy saw %#v, want %#v", rec.gotResources, want)
	}
	if rec.gotEnvironment != "development" {
		t.Errorf("policy saw environment %q, want development", rec.gotEnvironment)
	}
}

// --- reserved value paths ----------------------------------------------------

// The bypass this closes: values.sqldatabase renders a PostgresInstance XR
// directly. Left reachable, a caller could provision a billed Azure server with
// no resourcePolicy evaluation at all — and the tunable allowlist ships
// audit-first, so it would log the violation and allow it.
func TestReservedValueOverrides(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   []string
	}{
		{"clean overlay", map[string]any{"replicaCount": 2}, nil},
		{
			"the building block itself",
			map[string]any{"sqldatabase": map[string]any{"enabled": true}},
			[]string{"sqldatabase"},
		},
		{
			"an empty block still addresses it",
			map[string]any{"sqldatabase": map[string]any{}},
			[]string{"sqldatabase"},
		},
		{
			"the app's bind",
			map[string]any{"hex-scaffold": map[string]any{
				"postgres": map[string]any{"bindBuildingBlock": map[string]any{"instanceName": "someone-elses-db"}},
			}},
			[]string{"hex-scaffold.postgres.bindBuildingBlock"},
		},
		{
			"a legitimate hex-scaffold knob is untouched",
			map[string]any{"hex-scaffold": map[string]any{"replicaCount": 3}},
			nil,
		},
		{
			"both halves",
			map[string]any{
				"sqldatabase": map[string]any{"engine": "cloudnativepg"},
				"hex-scaffold": map[string]any{
					"postgres": map[string]any{"bindBuildingBlock": map[string]any{"enabled": false}},
				},
			},
			[]string{"sqldatabase", "hex-scaffold.postgres.bindBuildingBlock"},
		},
		{
			// The guard must not depend on the subchart's NAME. A scaffolded app's
			// subchart is called after the app, so a check written against the
			// literal `hex-scaffold` protected the template umbrella and left every
			// app built from it able to point itself at another team's database.
			"a scaffolded app's bind, under its own subchart key",
			map[string]any{appKey: map[string]any{
				"postgres": map[string]any{"bindBuildingBlock": map[string]any{"instanceName": "someone-elses-db"}},
			}},
			[]string{appKey + ".postgres.bindBuildingBlock"},
		},
		{
			"a legitimate knob under the app's own key is untouched",
			map[string]any{appKey: map[string]any{"replicaCount": 3}},
			nil,
		},
		{
			// Stable ordering: subchart keys are walked in sorted order, so two
			// identical refusals report identically.
			"two subcharts both reaching for a bind",
			map[string]any{
				"zzz-app": map[string]any{"postgres": map[string]any{"bindBuildingBlock": map[string]any{"enabled": true}}},
				"aaa-app": map[string]any{"postgres": map[string]any{"bindBuildingBlock": map[string]any{"enabled": true}}},
			},
			[]string{"aaa-app.postgres.bindBuildingBlock", "zzz-app.postgres.bindBuildingBlock"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReservedValueOverrides(tc.values); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("reservedOverrides = %v, want %v", got, tc.want)
			}
		})
	}
}
