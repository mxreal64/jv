// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mxreal64/jv/resolver"
)

// UpgradeCandidate is a dependency with a newer published version.
type UpgradeCandidate struct {
	Coordinate string // groupId:artifactId
	Current    string
	Latest     string
	Type       string // major, minor, patch or other
}

// VersionSource lists the published versions of an artifact; *resolver.Loader implements it from
// the repositories' maven-metadata.xml.
type VersionSource interface {
	Versions(group, artifact string) ([]string, error)
}

// CheckUpgrades looks every dependency up in its repositories' maven-metadata.xml. Lookup failures
// are returned (joined) together with the candidates that could be determined.
func CheckUpgrades(ctx context.Context, loader *resolver.Loader, deps []resolver.Artifact) ([]UpgradeCandidate, error) {
	return CheckUpgradesFrom(ctx, loader, deps)
}

// CheckUpgradesFrom is CheckUpgrades over any VersionSource.
func CheckUpgradesFrom(ctx context.Context, src VersionSource, deps []resolver.Artifact) ([]UpgradeCandidate, error) {
	type key struct{ g, a, v string }
	seen := map[key]bool{}
	var todo []key
	for _, d := range deps {
		k := key{d.GroupID, d.ArtifactID, d.Version}
		if d.Version == "" || strings.HasSuffix(d.Version, "-SNAPSHOT") || strings.ContainsAny(d.Version, "[(,") || seen[k] {
			continue // ranges and snapshots have no "current version" to compare with
		}
		seen[k] = true
		todo = append(todo, k)
	}

	var mu sync.Mutex
	var out []UpgradeCandidate
	var errs []error
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, k := range todo {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(k key) {
			defer wg.Done()
			defer func() { <-sem }()
			versions, err := src.Versions(k.g, k.a)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !errors.Is(err, resolver.ErrNotFound) { // not in any repository (an internal artifact): skipped
					errs = append(errs, fmt.Errorf("%s:%s: %w", k.g, k.a, err))
				}
				return
			}
			if latest := LatestVersion(k.v, versions); latest != "" {
				out = append(out, UpgradeCandidate{Coordinate: k.g + ":" + k.a, Current: k.v, Latest: latest, Type: ClassifyUpgrade(k.v, latest)})
			}
		}(k)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Coordinate < out[j].Coordinate })
	return out, errors.Join(errs...)
}

var unstableRE = regexp.MustCompile(`(?i)(alpha|beta|snapshot|preview|[.-]rc\d*|[.-]cr\d*|[.-]m\d+|[.-]dev|[.-]ea|[.-]pre|[.-]b\d+|[.-]a\d+)`)

func isStable(v string) bool { return !unstableRE.MatchString(v) }

// flavor is a build variant that must not change on upgrade (Guava publishes -jre and -android).
func flavor(v string) string {
	for _, f := range []string{"-jre", "-android"} {
		if strings.HasSuffix(v, f) {
			return f
		}
	}
	return ""
}

// LatestVersion returns the newest version in versions that is greater than current, or "" if there
// is none. Pre-releases are only considered when current is one itself, and a variant suffix such
// as -jre is kept.
func LatestVersion(current string, versions []string) string {
	best := ""
	for _, v := range versions {
		v = strings.TrimSpace(v)
		if v == "" || flavor(v) != flavor(current) {
			continue
		}
		if isStable(current) && !isStable(v) {
			continue
		}
		if resolver.CompareVersions(v, current) <= 0 {
			continue
		}
		if best == "" || resolver.CompareVersions(v, best) > 0 {
			best = v
		}
	}
	return best
}

func numericParts(v string) [3]int {
	var out [3]int
	fields := strings.FieldsFunc(v, func(r rune) bool { return r < '0' || r > '9' })
	for i := 0; i < len(fields) && i < 3; i++ {
		out[i], _ = strconv.Atoi(fields[i])
	}
	return out
}

// ClassifyUpgrade says whether latest is a major, minor or patch step from current.
func ClassifyUpgrade(current, latest string) string {
	c, l := numericParts(current), numericParts(latest)
	switch {
	case c[0] != l[0]:
		return "major"
	case c[1] != l[1]:
		return "minor"
	case c[2] != l[2]:
		return "patch"
	}
	return "other"
}

func FormatUpgradeSummary(upgrades []UpgradeCandidate) string {
	if len(upgrades) == 0 {
		return "All dependencies are up to date."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d upgrades available.\n", len(upgrades)))
	for _, u := range upgrades {
		sb.WriteString(fmt.Sprintf("  %s %s -> %s (%s)\n", u.Coordinate, u.Current, u.Latest, u.Type))
	}
	return sb.String()
}
