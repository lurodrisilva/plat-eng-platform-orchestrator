package port

import "testing"

// The application subchart is identified STRUCTURALLY — it is the umbrella's only
// file:// dependency — because its name is the unknown being looked up. Matching
// on a name would require the answer as input.
func TestAppValuesKeyOf(t *testing.T) {
	tests := []struct {
		name string
		deps []ChartDependency
		want string
	}{
		{
			// A scaffolded app: the scaffolder substituted `hex-scaffold` for the
			// app's own name, so the values key is the app's name. This is the case
			// production actually runs, and the case a hardcoded alias gets wrong.
			name: "scaffolded app, no alias",
			deps: []ChartDependency{
				{Name: "orders-v3", Repository: "file://../helm/orders-v3"},
				{Name: "plat-eng-sql-database-package", Alias: "sqldatabase", Repository: "oci://ghcr.io/o/helm-charts"},
			},
			want: "orders-v3",
		},
		{
			// The template's own umbrella — where the literal `hex-scaffold` came
			// from, and the only chart for which it was ever right.
			name: "template umbrella",
			deps: []ChartDependency{
				{Name: "hex-scaffold", Repository: "file://../helm/hex-scaffold"},
			},
			want: "hex-scaffold",
		},
		{
			// Helm keys values by the alias when there is one, so this rule must too.
			name: "alias wins over chart name",
			deps: []ChartDependency{
				{Name: "orders-v3", Alias: "app", Repository: "file://../helm/orders-v3"},
			},
			want: "app",
		},
		{
			// Building blocks resolve from a registry and are never the application.
			name: "only registry dependencies",
			deps: []ChartDependency{
				{Name: "plat-eng-sql-database-package", Alias: "sqldatabase", Repository: "oci://ghcr.io/o/helm-charts"},
				{Name: "plat-eng-commons-package", Repository: "https://charts.example.com"},
			},
			want: "",
		},
		{
			name: "no dependencies",
			deps: nil,
			want: "",
		},
		{
			// The first local dependency wins. An umbrella with two would be a
			// different chart shape than the platform builds, and guessing between
			// them is worse than the caller's refusal that an empty key triggers.
			name: "first local dependency wins",
			deps: []ChartDependency{
				{Name: "first", Repository: "file://../helm/first"},
				{Name: "second", Repository: "file://../helm/second"},
			},
			want: "first",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AppValuesKeyOf(tc.deps); got != tc.want {
				t.Errorf("AppValuesKeyOf = %q, want %q", got, tc.want)
			}
		})
	}
}
