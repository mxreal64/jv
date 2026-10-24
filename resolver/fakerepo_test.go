// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mxreal64/jv/config"
	"github.com/mxreal64/jv/pom"
)

// fakeRepo maps "group:artifact:version" to the inner XML of that POM's <dependencies>.
type fakeRepo map[string]string

func (f fakeRepo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	n := len(parts)
	if n < 4 || !strings.HasSuffix(parts[n-1], ".pom") {
		http.NotFound(w, r)
		return
	}
	a, v := parts[n-3], parts[n-2]
	g := strings.Join(parts[:n-3], ".")
	body, ok := f[g+":"+a+":"+v]
	if !ok {
		http.NotFound(w, r)
		return
	}
	fmt.Fprintf(w, `<project><modelVersion>4.0.0</modelVersion><groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version><dependencies>%s</dependencies></project>`, g, a, v, body)
}

func xdep(a, v, scope string, optional bool) string {
	s := fmt.Sprintf("<dependency><groupId>t</groupId><artifactId>%s</artifactId><version>%s</version>", a, v)
	if scope != "" {
		s += "<scope>" + scope + "</scope>"
	}
	if optional {
		s += "<optional>true</optional>"
	}
	return s + "</dependency>"
}

func newFakeLoader(t *testing.T, repo fakeRepo) *Loader {
	t.Helper()
	srv := httptest.NewServer(repo)
	t.Cleanup(srv.Close)
	s := &config.Settings{Mirrors: []config.Mirror{{ID: "fake", URL: srv.URL, MirrorOf: "*"}}}
	return NewLoader(context.Background(), t.TempDir(), s)
}

func td(a, v, scope string) pom.Dependency {
	if scope == "" {
		scope = "compile"
	}
	return pom.Dependency{GroupID: "t", ArtifactID: a, Version: v, Type: "jar", Scope: scope}
}

func resolveFake(t *testing.T, repo fakeRepo, root *Effective) map[string]*Node {
	t.Helper()
	l := newFakeLoader(t, repo)
	if root.Mgmt == nil {
		root.Mgmt = map[string]pom.Dependency{}
	}
	nodes, err := l.Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	out := map[string]*Node{}
	for _, n := range Flatten(nodes) {
		out[n.Dep.ArtifactID] = n
	}
	return out
}

func TestResolveNearestWins(t *testing.T) {
	repo := fakeRepo{
		"t:A:1": xdep("C", "1", "", false),
		"t:B:1": xdep("D", "1", "", false),
		"t:D:1": xdep("C", "2", "", false),
		"t:C:1": "", "t:C:2": "",
	}
	got := resolveFake(t, repo, &Effective{Deps: []pom.Dependency{td("A", "1", ""), td("B", "1", "")}})
	if got["C"] == nil || got["C"].Dep.Version != "1" {
		t.Fatalf("C = %+v, want version 1 (nearest wins)", got["C"])
	}
}

func TestResolveSkipsTestProvidedOptional(t *testing.T) {
	repo := fakeRepo{
		"t:A:1": xdep("T", "1", "test", false) + xdep("P", "1", "provided", false) + xdep("O", "1", "", true) + xdep("K", "1", "", false),
		"t:K:1": "",
	}
	got := resolveFake(t, repo, &Effective{Deps: []pom.Dependency{td("A", "1", "")}})
	for _, a := range []string{"T", "P", "O"} {
		if got[a] != nil {
			t.Errorf("%s must not be resolved transitively", a)
		}
	}
	if got["K"] == nil {
		t.Errorf("K (compile) must be resolved")
	}
}

func TestResolveExclusions(t *testing.T) {
	repo := fakeRepo{"t:A:1": xdep("X", "1", "", false), "t:X:1": ""}
	a := td("A", "1", "")
	a.Exclusions = []pom.Exclusion{{GroupID: "t", ArtifactID: "X"}}
	got := resolveFake(t, repo, &Effective{Deps: []pom.Dependency{a}})
	if got["X"] != nil {
		t.Errorf("X should be excluded")
	}
}

func TestResolveRootManagementOverridesTransitiveVersion(t *testing.T) {
	repo := fakeRepo{"t:A:1": xdep("C", "1", "", false), "t:C:9": ""}
	c := td("C", "9", "")
	root := &Effective{Deps: []pom.Dependency{td("A", "1", "")}, Mgmt: map[string]pom.Dependency{c.Key(): c}}
	got := resolveFake(t, repo, root)
	if got["C"] == nil || got["C"].Dep.Version != "9" {
		t.Fatalf("C = %+v, want managed version 9", got["C"])
	}
}

func TestResolveScopeWideningReachesChildren(t *testing.T) {
	// X is reached via a runtime path (depth 2) and a compile path (depth 3). Its widened
	// compile scope must also apply to its child Y, as in Maven.
	repo := fakeRepo{
		"t:R:1": xdep("X", "1", "", false),
		"t:B:1": xdep("M", "1", "", false),
		"t:M:1": xdep("X", "1", "", false),
		"t:X:1": xdep("Y", "1", "", false),
		"t:Y:1": "",
	}
	got := resolveFake(t, repo, &Effective{Deps: []pom.Dependency{td("R", "1", "runtime"), td("B", "1", "")}})
	if got["X"] == nil || got["X"].Scope != "compile" {
		t.Fatalf("X = %+v, want scope compile", got["X"])
	}
	if got["Y"] == nil || got["Y"].Scope != "compile" {
		t.Fatalf("Y = %+v, want scope compile (inherited from widened X)", got["Y"])
	}
}
