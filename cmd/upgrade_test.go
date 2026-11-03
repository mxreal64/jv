// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mxreal64/jv/resolver"
)

type fakeVersions map[string][]string

func (f fakeVersions) Versions(g, a string) ([]string, error) {
	v, ok := f[g+":"+a]
	if !ok {
		return nil, fmt.Errorf("%w: %s:%s", resolver.ErrNotFound, g, a)
	}
	return v, nil
}

func TestClassifyUpgrade(t *testing.T) {
	cases := [][3]string{
		{"1.2.3", "2.0.0", "major"},
		{"1.2.3", "1.3.0", "minor"},
		{"1.2.3", "1.2.9", "patch"},
		{"31.1-jre", "33.0.0-jre", "major"},
		{"2.9", "2.10", "minor"},
	}
	for _, c := range cases {
		if got := ClassifyUpgrade(c[0], c[1]); got != c[2] {
			t.Errorf("ClassifyUpgrade(%s, %s) = %s, want %s", c[0], c[1], got, c[2])
		}
	}
}

func TestLatestVersion(t *testing.T) {
	vs := []string{"1.0", "1.1", "1.2", "2.0.0-RC1", "2.0.0-M3", "1.10", "2.0.0-beta1"}
	if got := LatestVersion("1.1", vs); got != "1.10" {
		t.Errorf("stable current must ignore pre-releases and compare numerically, got %q", got)
	}
	if got := LatestVersion("1.10", vs); got != "" {
		t.Errorf("nothing newer: got %q", got)
	}
	if got := LatestVersion("2.0.0-M3", vs); got != "2.0.0-RC1" {
		t.Errorf("a pre-release user may move to a newer pre-release, got %q", got)
	}
	guava := []string{"31.1-jre", "32.0.0-android", "33.0.0-jre", "33.1.0-android"}
	if got := LatestVersion("31.1-jre", guava); got != "33.0.0-jre" {
		t.Errorf("flavor must be kept, got %q", got)
	}
}

func TestCheckUpgradesFrom(t *testing.T) {
	src := fakeVersions{
		"g:old":   {"1.0.0", "1.0.1", "1.1.0", "2.0.0"},
		"g:fresh": {"3.0.0"},
		"g:patch": {"1.0.0", "1.0.5"},
	}
	deps := []resolver.Artifact{
		{GroupID: "g", ArtifactID: "old", Version: "1.0.0"},
		{GroupID: "g", ArtifactID: "fresh", Version: "3.0.0"},
		{GroupID: "g", ArtifactID: "patch", Version: "1.0.0"},
		{GroupID: "g", ArtifactID: "patch", Version: "1.0.0"},  // duplicate
		{GroupID: "g", ArtifactID: "internal", Version: "1.0"}, // not in any repository: skipped
		{GroupID: "g", ArtifactID: "ranged", Version: "[1.0,2.0)"},
		{GroupID: "g", ArtifactID: "snap", Version: "1.0-SNAPSHOT"},
	}
	got, err := CheckUpgradesFrom(context.Background(), src, deps)
	if err != nil {
		t.Fatalf("unknown artifacts must be skipped silently, got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0] != (UpgradeCandidate{"g:old", "1.0.0", "2.0.0", "major"}) {
		t.Errorf("first = %+v", got[0])
	}
	if got[1] != (UpgradeCandidate{"g:patch", "1.0.0", "1.0.5", "patch"}) {
		t.Errorf("second = %+v", got[1])
	}
	out := FormatUpgradeSummary(got)
	if !strings.Contains(out, "2 upgrades available") || !strings.Contains(out, "g:old 1.0.0 -> 2.0.0 (major)") {
		t.Errorf("summary = %q", out)
	}
	if FormatUpgradeSummary(nil) != "All dependencies are up to date." {
		t.Error("empty summary")
	}
}

type failingVersions struct{}

func (failingVersions) Versions(g, a string) ([]string, error) {
	return nil, fmt.Errorf("network down")
}

func TestCheckUpgradesReportsLookupFailures(t *testing.T) {
	_, err := CheckUpgradesFrom(context.Background(), failingVersions{}, []resolver.Artifact{{GroupID: "g", ArtifactID: "a", Version: "1"}})
	if err == nil || !strings.Contains(err.Error(), "g:a") {
		t.Errorf("a failed lookup must be reported, got %v", err)
	}
}
