package github

import "testing"

func makeTags(names ...string) []Tag {
	tags := make([]Tag, len(names))
	for i, n := range names {
		tags[i] = Tag{Name: n}
	}
	return tags
}

func TestSelectHighestSemVerTag_Basic(t *testing.T) {
	tags := makeTags("v1.0.0", "v1.2.3", "v1.1.0", "v0.9.0")
	rawTag, version, err := SelectHighestSemVerTag(tags, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if rawTag != "v1.2.3" {
		t.Errorf("expected v1.2.3, got %s", rawTag)
	}
	if version != "1.2.3" {
		t.Errorf("expected 1.2.3, got %s", version)
	}
}

func TestSelectHighestSemVerTag_WithoutVPrefix(t *testing.T) {
	tags := makeTags("1.0.0", "2.0.0", "1.5.0")
	rawTag, version, err := SelectHighestSemVerTag(tags, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if rawTag != "2.0.0" {
		t.Errorf("expected 2.0.0, got %s", rawTag)
	}
	if version != "2.0.0" {
		t.Errorf("expected 2.0.0, got %s", version)
	}
}

func TestSelectHighestSemVerTag_SkipsPrerelease(t *testing.T) {
	tags := makeTags("v1.0.0", "v2.0.0-rc.1", "v1.5.0")
	rawTag, _, err := SelectHighestSemVerTag(tags, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if rawTag != "v1.5.0" {
		t.Errorf("expected v1.5.0 (skip prerelease), got %s", rawTag)
	}
}

func TestSelectHighestSemVerTag_AllowsPrerelease(t *testing.T) {
	tags := makeTags("v1.0.0", "v2.0.0-rc.1", "v1.5.0")
	rawTag, _, err := SelectHighestSemVerTag(tags, "", true)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-release versions are lower than the release in semver, but 2.0.0-rc.1 > 1.5.0
	if rawTag != "v2.0.0-rc.1" {
		t.Errorf("expected v2.0.0-rc.1 with prerelease allowed, got %s", rawTag)
	}
}

func TestSelectHighestSemVerTag_SkipsNonSemVer(t *testing.T) {
	tags := makeTags("latest", "v1.0.0", "main", "v2.0.0", "not-a-version")
	rawTag, _, err := SelectHighestSemVerTag(tags, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if rawTag != "v2.0.0" {
		t.Errorf("expected v2.0.0, got %s", rawTag)
	}
}

func TestSelectHighestSemVerTag_NoValidTags(t *testing.T) {
	tags := makeTags("latest", "main", "not-a-version")
	_, _, err := SelectHighestSemVerTag(tags, "", false)
	if err == nil {
		t.Error("expected error for no valid tags")
	}
}

func TestSelectHighestSemVerTag_EmptyList(t *testing.T) {
	_, _, err := SelectHighestSemVerTag(nil, "", false)
	if err == nil {
		t.Error("expected error for empty tag list")
	}
}

func TestSelectHighestSemVerTag_TildeConstraint(t *testing.T) {
	tags := makeTags("v1.4.0", "v1.5.0", "v1.5.3", "v1.6.0", "v2.0.0")
	rawTag, _, err := SelectHighestSemVerTag(tags, "~1.5.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if rawTag != "v1.5.3" {
		t.Errorf("expected v1.5.3 (~1.5.0 constraint), got %s", rawTag)
	}
}

func TestSelectHighestSemVerTag_CaretConstraint(t *testing.T) {
	tags := makeTags("v1.4.0", "v1.5.0", "v1.9.0", "v2.0.0", "v3.0.0")
	rawTag, _, err := SelectHighestSemVerTag(tags, "^1.4.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if rawTag != "v1.9.0" {
		t.Errorf("expected v1.9.0 (^1.4.0 constraint), got %s", rawTag)
	}
}

func TestSelectHighestSemVerTag_ExactConstraint(t *testing.T) {
	tags := makeTags("v1.0.0", "v1.5.0", "v2.0.0")
	rawTag, _, err := SelectHighestSemVerTag(tags, "1.5.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if rawTag != "v1.5.0" {
		t.Errorf("expected v1.5.0 (exact constraint), got %s", rawTag)
	}
}

func TestSelectHighestSemVerTag_Deterministic(t *testing.T) {
	tags := makeTags("v1.0.0", "v1.2.3", "v1.1.0")

	// Run multiple times — result must always be the same
	for i := 0; i < 10; i++ {
		rawTag, _, err := SelectHighestSemVerTag(tags, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if rawTag != "v1.2.3" {
			t.Errorf("iteration %d: expected deterministic v1.2.3, got %s", i, rawTag)
		}
	}
}
