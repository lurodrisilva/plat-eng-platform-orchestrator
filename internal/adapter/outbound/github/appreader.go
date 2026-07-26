package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/myorg/platform-orchestrator/internal/application/port"
)

// umbrellaChartPath is where a repository rendered from the net-hexagonal
// template keeps its deploy unit (ADR-0008).
const umbrellaChartPath = "deploy/umbrella/Chart.yaml"

// contentsResponse is the subset of the GitHub contents object we consume. The
// shared doRequest sends `Accept: application/vnd.github+json`, so content
// arrives base64-encoded rather than raw.
type contentsResponse struct {
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

// chartYAML is the subset of a Helm Chart.yaml this adapter reads.
type chartYAML struct {
	Name         string `json:"name"`
	Dependencies []struct {
		Name       string `json:"name"`
		Alias      string `json:"alias"`
		Repository string `json:"repository"`
	} `json:"dependencies"`
}

// ReadUmbrellaChart fetches deploy/umbrella/Chart.yaml from the repository's
// default branch and reports the chart name plus the values key its application
// subchart hangs under.
//
// 404 (no such file, or no such repository) yields a zero UmbrellaChart and no
// error: an app polled between `POST /apps` and the scaffold workflow's push has
// no chart yet, and that is a state to report rather than a failure.
func (s *Scaffolder) ReadUmbrellaChart(ctx context.Context, repo string) (port.UmbrellaChart, error) {
	if !nameRE.MatchString(repo) {
		return port.UmbrellaChart{}, fmt.Errorf("invalid app name %q: must be lowercase alphanumeric and hyphens, 1-39 chars", repo)
	}

	token, err := s.auth.installationToken(ctx)
	if err != nil {
		return port.UmbrellaChart{}, fmt.Errorf("read umbrella chart: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", s.baseURL, s.newRepoOwner, repo, umbrellaChartPath)
	status, body, err := s.doRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return port.UmbrellaChart{}, fmt.Errorf("read umbrella chart: %w", err)
	}
	if status == http.StatusNotFound {
		return port.UmbrellaChart{}, nil
	}
	if status != http.StatusOK {
		return port.UmbrellaChart{}, fmt.Errorf("read umbrella chart: github returned %d: %s", status, string(body))
	}

	var contents contentsResponse
	if err := json.Unmarshal(body, &contents); err != nil {
		return port.UmbrellaChart{}, fmt.Errorf("read umbrella chart: decode contents: %w", err)
	}
	if contents.Encoding != "base64" {
		return port.UmbrellaChart{}, fmt.Errorf("read umbrella chart: unexpected content encoding %q", contents.Encoding)
	}
	// GitHub wraps base64 content at 60 columns; the standard decoder rejects
	// the newlines, so strip whitespace before decoding.
	raw, err := base64.StdEncoding.DecodeString(strings.Map(dropWhitespace, contents.Content))
	if err != nil {
		return port.UmbrellaChart{}, fmt.Errorf("read umbrella chart: decode base64: %w", err)
	}

	var chrt chartYAML
	if err := yaml.Unmarshal(raw, &chrt); err != nil {
		return port.UmbrellaChart{}, fmt.Errorf("read umbrella chart: parse %s: %w", umbrellaChartPath, err)
	}
	if chrt.Name == "" {
		return port.UmbrellaChart{}, fmt.Errorf("read umbrella chart: %s has no name", umbrellaChartPath)
	}

	return port.UmbrellaChart{ChartName: chrt.Name, AppValuesKey: appValuesKey(chrt)}, nil
}

// appValuesKey picks the values key the application subchart hangs under.
//
// The application is the umbrella's LOCAL dependency: it ships inside the app
// repository and is referenced as `file://../helm/<app>`, while every building
// block resolves from an OCI registry. That distinction is the identification —
// it holds whatever the app is called, whereas matching on a name would need the
// app's identity, which is the thing being looked up.
//
// Helm keys a subchart's values by its alias when it has one and by its chart
// name otherwise, so the same rule is applied here.
func appValuesKey(chrt chartYAML) string {
	for _, d := range chrt.Dependencies {
		if !strings.HasPrefix(d.Repository, "file://") {
			continue
		}
		if d.Alias != "" {
			return d.Alias
		}
		return d.Name
	}
	return ""
}

func dropWhitespace(r rune) rune {
	if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
		return -1
	}
	return r
}

// compile-time proof the adapter satisfies the port.
var _ port.AppRepoReader = (*Scaffolder)(nil)
