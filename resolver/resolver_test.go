// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxreal64/jv/pom"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   int // -1 (v1 < v2), 0 (v1 == v2), 1 (v1 > v2)
	}{
		{"1.0", "1.0", 0},
		{"1.0.0", "1.0", 0},
		{"1.0", "1.1", -1},
		{"1.2", "1.1", 1},
		{"1.0-alpha-1", "1.0", -1},
		{"1.0-alpha-1", "1.0-beta-1", -1},
		{"1.0-beta-2", "1.0-beta-1", 1},
		{"1.0-rc-1", "1.0", -1},
		{"1.0-SNAPSHOT", "1.0", -1},
		{"1.0-sp", "1.0", 1},
		{"1.0-preview", "1.0", 1}, // like Maven, unrecognized qualifiers sort after the release
		{"1.0-preview", "1.0-alpha", 1},
		{"1.0-android", "1.0-jre", -1},
		{"32.0.0-android", "32.0-jre", -1},
		{"1.0-alpha", "1.0.0-alpha", 0},
		{"1.0-ga", "1.0", 0},
		{"1.0.1", "1.0", 1},
		{"2.0.1", "2.0.0", 1},
		{"2.1", "2.1.0", 0},
	}

	for _, tt := range tests {
		got := compareVersions(tt.v1, tt.v2)
		if (tt.want < 0 && got >= 0) || (tt.want > 0 && got <= 0) || (tt.want == 0 && got != 0) {
			t.Errorf("compareVersions(%q, %q) = %d; want sign %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

func TestVersionRangeContains(t *testing.T) {
	sets, err := parseRanges("[1.0, 2.0)")
	if err != nil {
		t.Fatalf("parseRanges failed: %v", err)
	}
	if len(sets) != 1 {
		t.Fatalf("expected 1 range set, got %d", len(sets))
	}
	rs := sets[0]

	if !rs.contains("1.0") {
		t.Errorf("expected [1.0, 2.0) to contain 1.0")
	}
	if !rs.contains("1.5.0") {
		t.Errorf("expected [1.0, 2.0) to contain 1.5.0")
	}
	if rs.contains("2.0") {
		t.Errorf("expected [1.0, 2.0) NOT to contain 2.0")
	}
	if rs.contains("0.9") {
		t.Errorf("expected [1.0, 2.0) NOT to contain 0.9")
	}
}

func TestWriteAndReadLock(t *testing.T) {
	tmp := t.TempDir()
	lockPath := filepath.Join(tmp, ".jv", "lock.json")
	jarPath := filepath.Join(tmp, "dummy.jar")
	if err := os.WriteFile(jarPath, []byte("jarcontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	arts := []Artifact{
		{GroupID: "g", ArtifactID: "a", Version: "1.0", Type: "jar", Scope: "compile", Path: jarPath},
	}
	key := "test-key-123"

	if err := WriteLock(lockPath, key, arts); err != nil {
		t.Fatalf("WriteLock failed: %v", err)
	}

	readArts, ok := ReadLock(lockPath, key)
	if !ok {
		t.Fatalf("ReadLock returned ok=false")
	}
	if len(readArts) != 1 || readArts[0].ArtifactID != "a" {
		t.Fatalf("unexpected read artifacts: %v", readArts)
	}

	// Mismatched key
	if _, ok := ReadLock(lockPath, "different-key"); ok {
		t.Errorf("ReadLock should fail on mismatched key")
	}

	// Missing file
	_ = os.Remove(jarPath)
	if _, ok := ReadLock(lockPath, key); ok {
		t.Errorf("ReadLock should fail when jar file on disk is removed")
	}
}

func TestOrderRejectsSelfDependency(t *testing.T) {
	self := &Module{Eff: &Effective{G: "t", A: "a", V: "1", Deps: []pom.Dependency{td("a", "1", "test")}}}
	if _, err := Order([]*Module{self}); err == nil || !strings.Contains(err.Error(), "depends on itself") {
		t.Fatalf("want a self-dependency error, got %v", err)
	}
	// An ordinary sibling dependency is still fine.
	b := &Module{Eff: &Effective{G: "t", A: "b", V: "1", Deps: []pom.Dependency{td("a", "1", "")}}}
	ok := &Module{Eff: &Effective{G: "t", A: "a", V: "1"}}
	out, err := Order([]*Module{b, ok})
	if err != nil || len(out) != 2 || out[0] != ok {
		t.Fatalf("order: %v %v", out, err)
	}
}

// Cases taken from the behaviour of Maven's ComparableVersion.
func TestCompareVersionsMavenParity(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1", "1.0", 0}, {"1-1", "1", 1}, {"1-1", "1.1", -1}, {"1-sp", "1-1", -1},
		{"1.0-rc1", "1", -1}, {"1.0-sp1", "1.0", 1}, {"1.0000001", "1.1", 0},
		{"1.99999999999999999999", "1.9", 1}, {"1.0-b1", "1.0-beta-1", 0},
		{"1.0-m1", "1.0-milestone-1", 0}, {"1.0-cr1", "1.0-rc1", 0},
		{"1.0-alpha-1", "1.0-beta-1", -1}, {"1.0-final", "1.0", 0}, {"1.0.RELEASE", "1.0", 0},
		{"2.0-SNAPSHOT", "2.0-rc-1", 1}, {"31.1-jre", "32.0.0-android", -1},
	} {
		if got := compareVersions(c.a, c.b); (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) {
			t.Errorf("compareVersions(%q,%q)=%d want sign %d", c.a, c.b, got, c.want)
		}
		if got := compareVersions(c.b, c.a); (got < 0) != (c.want > 0) || (got > 0) != (c.want < 0) {
			t.Errorf("compareVersions(%q,%q)=%d want sign %d", c.b, c.a, got, -c.want)
		}
	}
}
