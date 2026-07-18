package oci

import "testing"

func TestSelectVersion(t *testing.T) {
	// The real shape of the umbrella's published tags: only prereleases exist
	// so far, plus a stray non-semver tag to prove it is ignored.
	tags := []string{
		"0.1.0",
		"0.2.0-sha-6d234ca",
		"0.2.0-sha-f9f1f60",
		"latest", // non-semver; must be skipped, not error
	}

	cases := []struct {
		name       string
		tags       []string
		constraint string
		allowPre   bool
		want       string
		wantErr    bool
	}{
		{
			name:       "highest overall with star includes prereleases",
			tags:       tags,
			constraint: "*",
			allowPre:   true,
			want:       "0.2.0-sha-f9f1f60", // f > 6 lexically in the sha, but semver ranks the prerelease ids
		},
		{
			name:       "empty constraint equals star",
			tags:       tags,
			constraint: "",
			allowPre:   true,
			want:       "0.2.0-sha-f9f1f60",
		},
		{
			name:       "prerelease bound matches master builds",
			tags:       tags,
			constraint: ">=0.2.0-0",
			allowPre:   true,
			want:       "0.2.0-sha-f9f1f60",
		},
		{
			name:       "exact prerelease pins itself",
			tags:       tags,
			constraint: "0.2.0-sha-6d234ca",
			allowPre:   true,
			want:       "0.2.0-sha-6d234ca",
		},
		{
			name:       "non-prerelease bound excludes master builds (the release guarantee)",
			tags:       tags,
			constraint: ">=0.2.0",
			allowPre:   false,
			wantErr:    true, // only prereleases exist above 0.1.0
		},
		{
			name:       "stable constraint picks the stable tag",
			tags:       tags,
			constraint: "<0.2.0",
			allowPre:   false,
			want:       "0.1.0",
		},
		{
			name:       "prereleases excluded by default even when present",
			tags:       []string{"0.1.0", "0.2.0-sha-abc1234"},
			constraint: "*",
			allowPre:   false,
			want:       "0.1.0",
		},
		{
			name:       "no candidates is an error, not a panic",
			tags:       []string{"not-semver", "also-bad"},
			constraint: "*",
			allowPre:   true,
			wantErr:    true,
		},
		{
			name:       "invalid constraint is an error",
			tags:       tags,
			constraint: "not a constraint",
			allowPre:   true,
			wantErr:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectVersion(tc.tags, tc.constraint, tc.allowPre)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("selectVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPath(t *testing.T) {
	cases := map[string]string{}
	cases["ghcr.io/x/charts|umbrella"] = "ghcr.io/x/charts/umbrella"
	cases["oci://ghcr.io/x/charts|umbrella"] = "ghcr.io/x/charts/umbrella"
	cases["ghcr.io/x/charts/|umbrella"] = "ghcr.io/x/charts/umbrella"
	for in, want := range cases {
		var repo, name string
		for i := 0; i < len(in); i++ {
			if in[i] == '|' {
				repo, name = in[:i], in[i+1:]
				break
			}
		}
		if got := path(repo, name); got != want {
			t.Errorf("path(%q,%q) = %q, want %q", repo, name, got, want)
		}
	}
}
