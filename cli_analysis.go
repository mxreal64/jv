// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mxreal64/jv/cmd"
	"github.com/mxreal64/jv/resolver"
)

// analysisGoals only read the resolved dependency graph; they never build anything.
var analysisGoals = map[string]bool{"audit": true, "upgrade": true, "explain": true}

func hasAnalysisGoal(goals []string) bool {
	for _, g := range goals {
		if analysisGoals[g] {
			return true
		}
	}
	return false
}

var nonArtifactChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// runInit scaffolds a new Maven project in dir.
func runInit(o *cliOpts, dir string) int {
	artifact := o.props["artifactId"]
	if artifact == "" {
		artifact = strings.Trim(nonArtifactChars.ReplaceAllString(strings.ToLower(filepath.Base(dir)), "-"), "-.")
	}
	group, version := o.props["groupId"], o.props["version"]
	if err := cmd.InitProject(dir, group, artifact, version, o.props["type"]); err != nil {
		errf("init: %v", err)
		return 1
	}
	logf("created a Maven project (%s) in %s", artifactOrDefault(artifact), dir)
	logf("next: jv test   (edit src/main/java, then jv package)")
	return 0
}

func artifactOrDefault(a string) string {
	if a == "" {
		return "myapp"
	}
	return a
}

// depArtifacts turns resolved nodes into artifact coordinates (no download needed).
func depArtifacts(nodes []*resolver.Node, all bool) []resolver.Artifact {
	list := nodes
	if all {
		list = resolver.Flatten(nodes)
	}
	var out []resolver.Artifact
	for _, n := range list {
		out = append(out, resolver.Artifact{GroupID: n.Dep.GroupID, ArtifactID: n.Dep.ArtifactID, Version: n.Dep.Version, Scope: n.Scope})
	}
	return out
}

// runAnalysis runs audit, upgrade or explain over the selected modules and returns the exit code:
// 0 ok, 1 findings (audit) or no match (explain), 2 the analysis itself failed.
func runAnalysis(ctx context.Context, loader *resolver.Loader, targets []*resolver.Module, goal string, o *cliOpts) int {
	type modNodes struct {
		m     *resolver.Module
		nodes []*resolver.Node
	}
	var mods []modNodes
	for _, m := range targets {
		if m.Eff.Packaging == "pom" && len(m.Eff.Deps) == 0 {
			continue
		}
		mlog := func(format string, a ...any) { logf("["+m.Eff.A+"] "+format, a...) }
		_, nodes, err := resolveModule(loader, m, true, mlog)
		if err != nil {
			errf("%s: %v", m.Eff.A, err)
			return 2
		}
		mods = append(mods, modNodes{m, nodes})
	}

	switch goal {
	case "explain":
		found := false
		for _, mn := range mods {
			paths := resolver.Explain(mn.nodes, o.query)
			if len(paths) == 0 {
				continue
			}
			found = true
			fmt.Printf("%s:%s:%s\n", mn.m.Eff.G, mn.m.Eff.A, mn.m.Eff.V)
			for _, p := range paths {
				fmt.Printf("  %s\n", resolver.FormatPath(p))
			}
			if reqs := resolver.Requirers(mn.nodes, o.query); len(reqs) > 0 {
				fmt.Println("  declared by:")
				for _, r := range reqs {
					wants := r.Wants
					if wants == "" {
						wants = "(managed version)"
					}
					fmt.Printf("    %s wants %s\n", r.By, wants)
				}
			}
		}
		if !found {
			errf("no dependency matching %q is on the resolved classpath", o.query)
			return 1
		}
		return 0

	case "upgrade":
		var arts []resolver.Artifact
		for _, mn := range mods {
			arts = append(arts, depArtifacts(mn.nodes, o.props["upgrade.all"] == "true")...)
		}
		cands, err := cmd.CheckUpgrades(ctx, loader, arts)
		if o.props["upgrade.json"] == "true" {
			b, _ := json.MarshalIndent(cands, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Print(cmd.FormatUpgradeSummary(cands))
		}
		if err != nil {
			errf("some versions could not be looked up:\n%v", err)
			if len(cands) == 0 {
				return 2
			}
		}
		return 0

	case "audit":
		if o.offline {
			errf("audit needs the network (OSV database); drop -o")
			return 2
		}
		var arts []resolver.Artifact
		for _, mn := range mods {
			arts = append(arts, depArtifacts(mn.nodes, true)...)
		}
		findings, err := cmd.AuditDependencies(ctx, arts)
		if err != nil {
			errf("audit failed: %v", err)
			return 2
		}
		return reportAudit(findings, len(arts), o)
	}
	return 0
}

// reportAudit prints findings and returns the exit code for -Daudit.failOn (none|low|medium|high|critical,
// default low: any finding fails the build).
func reportAudit(findings []cmd.AuditFinding, scanned int, o *cliOpts) int {
	if o.props["audit.json"] == "true" {
		if findings == nil {
			findings = []cmd.AuditFinding{}
		}
		b, _ := json.MarshalIndent(findings, "", "  ")
		fmt.Println(string(b))
	} else if len(findings) == 0 {
		fmt.Printf("No known vulnerabilities in %d dependencies.\n", scanned)
	} else {
		pkgs := map[string]bool{}
		for _, f := range findings {
			pkgs[f.Package+"@"+f.Version] = true
			fmt.Printf("%-8s %-22s %s@%s  %s\n", f.Severity, f.ID, f.Package, f.Version, f.Summary)
		}
		var names []string
		for p := range pkgs {
			names = append(names, p)
		}
		sort.Strings(names)
		fmt.Printf("%d known vulnerabilities in %d of %d dependencies.\n", len(findings), len(names), scanned)
	}

	failOn := strings.ToLower(o.props["audit.failOn"])
	if failOn == "none" {
		return 0
	}
	min := 0 // default: any finding, including those without a severity
	if failOn != "" && failOn != "low" {
		if min = cmd.SeverityRank(failOn); min == 0 {
			errf("audit.failOn must be none, low, medium, high or critical, got %q", failOn)
			return 2
		}
	}
	for _, f := range findings {
		if min == 0 || cmd.SeverityRank(f.Severity) >= min {
			return 1
		}
	}
	return 0
}
