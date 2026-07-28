package oci

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"oras.land/oras-go/v2/registry/remote/errcode"
)

func TestAppImage_ReadsThePinnedImageUnderTheAppKey(t *testing.T) {
	// A published umbrella's values: the app subchart is keyed by the APP's name
	// because the scaffolder renamed it, and release CI pinned the digest there.
	values := map[string]any{
		"sqldatabase": map[string]any{"enabled": true},
		"orders-v3": map[string]any{
			"image": map[string]any{
				"repository": "ghcr.io/lurodrisilva/orders-v3-fcdc",
				"tag":        "latest",
				"digest":     "sha256:deadbeef",
			},
		},
	}

	repo, tag, digest := appImage(values, "orders-v3")
	if repo != "ghcr.io/lurodrisilva/orders-v3-fcdc" {
		t.Errorf("repository = %q", repo)
	}
	if tag != "latest" {
		t.Errorf("tag = %q", tag)
	}
	if digest != "sha256:deadbeef" {
		t.Errorf("digest = %q", digest)
	}
}

// The failure this guards: addressing the app's values by the template's alias.
// It returns nothing rather than panicking, and the caller reports an empty
// image rather than a wrong one.
func TestAppImage_WrongKeyYieldsNothing(t *testing.T) {
	values := map[string]any{
		"orders-v3": map[string]any{"image": map[string]any{"repository": "ghcr.io/o/orders-v3"}},
	}
	if repo, _, _ := appImage(values, "hex-scaffold"); repo != "" {
		t.Errorf("repository = %q, want empty for a key the chart does not have", repo)
	}
	if repo, _, _ := appImage(values, ""); repo != "" {
		t.Errorf("repository = %q, want empty for an unknown key", repo)
	}
}

// A chart published before the digest pin existed has a version worth reporting
// and no digest. Erroring would hide the version that WAS found.
func TestAppImage_MissingPiecesAreEmptyNotFatal(t *testing.T) {
	values := map[string]any{"orders-v3": map[string]any{"image": map[string]any{"tag": "latest"}}}
	repo, tag, digest := appImage(values, "orders-v3")
	if repo != "" || digest != "" || tag != "latest" {
		t.Errorf("got (%q,%q,%q), want only the tag", repo, tag, digest)
	}

	// Values shaped unexpectedly must not panic either.
	if r, _, _ := appImage(map[string]any{"orders-v3": "not-a-map"}, "orders-v3"); r != "" {
		t.Errorf("repository = %q, want empty", r)
	}
	if r, _, _ := appImage(map[string]any{"orders-v3": map[string]any{"image": 7}}, "orders-v3"); r != "" {
		t.Errorf("repository = %q, want empty", r)
	}
}

func TestIsNotPublished(t *testing.T) {
	// `want` is the anonymous verdict, `wantAuthed` the verdict once a credential
	// is configured for the host. They differ exactly where the ambiguity lives:
	// anonymous, GHCR's 401 could mean private-or-absent; authenticated, it can
	// only mean the credential is bad, and calling that "not published" hides it.
	tests := []struct {
		name       string
		err        error
		want       bool
		wantAuthed bool
	}{
		{
			// GHCR's actual answer for a chart nobody has pushed: the token
			// exchange happens before existence is checked, so an anonymous
			// client is told "unauthorized" rather than "not found".
			name:       "401 from GHCR for an unpublished chart",
			err:        &errcode.ErrorResponse{StatusCode: http.StatusUnauthorized},
			want:       true,
			wantAuthed: false,
		},
		{
			name:       "403",
			err:        &errcode.ErrorResponse{StatusCode: http.StatusForbidden},
			want:       true,
			wantAuthed: false,
		},
		{
			// 404 is unambiguous either way — the registry answered, and the
			// answer was that nothing is there.
			name:       "404 from a registry that answers honestly",
			err:        &errcode.ErrorResponse{StatusCode: http.StatusNotFound},
			want:       true,
			wantAuthed: true,
		},
		{
			// The distinction that matters: an outage must not read as "your app
			// has not built yet", or the caller waits for a build that finished.
			name: "503 is an outage, not an unpublished chart",
			err:  &errcode.ErrorResponse{StatusCode: http.StatusServiceUnavailable},
		},
		{
			name: "a plain error is not a registry verdict",
			err:  errors.New("dial tcp: connection refused"),
		},
		{
			// Wrapped errors still classify — helm returns the oras error, but a
			// future wrap must not silently turn every 401 into a hard failure.
			name: "wrapped 401",
			err:  fmt.Errorf("list tags: %w", &errcode.ErrorResponse{StatusCode: http.StatusUnauthorized}),
			want: true,
		},
	}

	const repoRef = "ghcr.io/lurodrisilva/helm-charts/orders-v4-umbrella"
	anon := &Resolver{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	authed := &Resolver{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		creds:    map[string]CredentialFunc{"ghcr.io": StaticCredential("u", "t")},
		loggedIn: map[string]bool{},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := anon.isNotPublished(repoRef, tc.err); got != tc.want {
				t.Errorf("anonymous isNotPublished(%v) = %t, want %t", tc.err, got, tc.want)
			}
			if got := authed.isNotPublished(repoRef, tc.err); got != tc.wantAuthed {
				t.Errorf("authenticated isNotPublished(%v) = %t, want %t", tc.err, got, tc.wantAuthed)
			}
		})
	}

	// A credential for a DIFFERENT host must not change the verdict for this one.
	// Otherwise configuring GHCR access would quietly re-classify ACR's 401s too.
	other := &Resolver{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		creds:    map[string]CredentialFunc{"acrdev.azurecr.io": StaticCredential("u", "t")},
		loggedIn: map[string]bool{},
	}
	unauth := &errcode.ErrorResponse{StatusCode: http.StatusUnauthorized}
	if !other.isNotPublished(repoRef, unauth) {
		t.Error("a credential for another host must leave this host's 401 ambiguous")
	}
}

// Provenance comes from the chart's appVersion, and only when it is actually a
// commit. The live registry publishes chart 0.3.0-sha-35a48cd with appVersion
// 35a48cd, which is the case that must work.
func TestShortSHA(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"35a48cd", "35a48cd"}, // a real published appVersion
		{"9f2c1a4e8b7d6053f1e2a3b4c5d6e7f809123456", "9f2c1a4e8b7d6053f1e2a3b4c5d6e7f809123456"}, // full sha
		{"latest", ""},  // the template chart's own appVersion
		{"1.2.3", ""},   // a tagged release publishes the semver here
		{"abc12", ""},   // too short to be a usable short sha
		{"", ""},        // no appVersion at all
		{"35a48cg", ""}, // not hex
		{"35A48CD", ""}, // git abbreviates lowercase; uppercase is something else
	}
	for _, tc := range tests {
		if got := shortSHA(tc.in); got != tc.want {
			t.Errorf("shortSHA(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveLatest_RequiresRepositoryAndChartName(t *testing.T) {
	r := &Resolver{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := r.ResolveLatest(t.Context(), "", "orders-v3-umbrella", "orders-v3"); err == nil {
		t.Error("expected an error for an empty repository")
	}
	if _, err := r.ResolveLatest(t.Context(), "ghcr.io/o/helm-charts", "", "orders-v3"); err == nil {
		t.Error("expected an error for an empty chart name")
	}
}
