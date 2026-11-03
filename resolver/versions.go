// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"fmt"
	"strings"

	"github.com/mxreal64/jv/pom"
)

// CompareVersions orders two version strings exactly like Maven's ComparableVersion.
func CompareVersions(a, b string) int { return compareVersions(a, b) }

// Versions lists every published version of g:a according to the configured repositories.
func (l *Loader) Versions(g, a string) ([]string, error) { return l.metadata(g, a) }

func depMatches(d pom.Dependency, q string) bool {
	parts := strings.Split(q, ":")
	switch len(parts) {
	case 1:
		return d.ArtifactID == parts[0]
	case 2:
		return d.GroupID == parts[0] && d.ArtifactID == parts[1]
	default:
		return d.GroupID == parts[0] && d.ArtifactID == parts[1] && d.Version == parts[len(parts)-1]
	}
}

// Explain returns every path through the resolved tree (outermost dependency first) that ends at a
// dependency matching query: "artifactId", "groupId:artifactId" or "groupId:artifactId:version".
func Explain(roots []*Node, query string) [][]*Node {
	query = strings.TrimSpace(query)
	var out [][]*Node
	var walk func(ns []*Node, path []*Node)
	walk = func(ns []*Node, path []*Node) {
		for _, n := range ns {
			p := append(append([]*Node(nil), path...), n)
			if depMatches(n.Dep, query) {
				out = append(out, p)
			}
			walk(n.Children, p)
		}
	}
	walk(roots, nil)
	return out
}

// FormatPath renders a path returned by Explain as "g:a:v -> g:a:v".
func FormatPath(path []*Node) string {
	parts := make([]string, len(path))
	for i, n := range path {
		parts[i] = fmt.Sprintf("%s:%s:%s", n.Dep.GroupID, n.Dep.ArtifactID, n.Dep.Version)
	}
	return strings.Join(parts, " -> ")
}

// Requirer is a resolved dependency whose own POM asks for the explained dependency.
type Requirer struct {
	By      string // groupId:artifactId:version of the dependent
	Wants   string // the version it declares ("" when managed elsewhere)
	OnPath  bool   // true when the dependent is on one of the paths Explain returned
	Pointer *Node  // the dependent
}

func declMatches(d pom.Dependency, q string) bool {
	parts := strings.Split(strings.TrimSpace(q), ":")
	if len(parts) == 1 {
		return d.ArtifactID == parts[0]
	}
	return d.GroupID == parts[0] && d.ArtifactID == parts[1]
}

// Requirers lists every resolved dependency whose POM declares a dependency matching query
// (groupId:artifactId, or just artifactId; the version is ignored). Unlike Explain it also finds
// declarations that mediation discarded (nearest wins), so it shows everything that asked for the
// dependency and which version each one wanted.
func Requirers(roots []*Node, query string) []Requirer {
	onPath := map[*Node]bool{}
	for _, p := range Explain(roots, query) {
		for _, n := range p[:len(p)-1] {
			onPath[n] = true
		}
	}
	var out []Requirer
	for _, n := range Flatten(roots) {
		if n.eff == nil {
			continue
		}
		for _, d := range n.eff.Deps {
			if declMatches(d, query) {
				out = append(out, Requirer{
					By:      fmt.Sprintf("%s:%s:%s", n.Dep.GroupID, n.Dep.ArtifactID, n.Dep.Version),
					Wants:   d.Version,
					OnPath:  onPath[n],
					Pointer: n,
				})
				break
			}
		}
	}
	return out
}
