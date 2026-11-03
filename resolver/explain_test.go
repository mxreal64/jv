// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"strings"
	"testing"

	"github.com/mxreal64/jv/pom"
)

func TestExplainFindsEveryPath(t *testing.T) {
	leaf := &Node{Dep: pom.Dependency{GroupID: "t", ArtifactID: "leaf", Version: "1"}}
	mid := &Node{Dep: pom.Dependency{GroupID: "t", ArtifactID: "mid", Version: "2"}, Children: []*Node{leaf}}
	other := &Node{Dep: pom.Dependency{GroupID: "t", ArtifactID: "other", Version: "3"}, Children: []*Node{
		{Dep: pom.Dependency{GroupID: "t", ArtifactID: "leaf", Version: "1"}},
	}}
	roots := []*Node{mid, other}

	for _, q := range []string{"leaf", "t:leaf", "t:leaf:1"} {
		if paths := Explain(roots, q); len(paths) != 2 {
			t.Fatalf("query %q: got %d paths, want 2", q, len(paths))
		}
	}
	if got := FormatPath(Explain(roots, "t:leaf")[0]); got != "t:mid:2 -> t:leaf:1" {
		t.Errorf("FormatPath = %q", got)
	}
	if len(Explain(roots, "t:leaf:9")) != 0 || len(Explain(roots, "nope")) != 0 {
		t.Error("non-matching queries must return nothing")
	}
	if !strings.Contains(FormatPath(Explain(roots, "other")[0]), "t:other:3") {
		t.Error("direct dependency path")
	}
}

func TestRequirersIncludeMediatedDeclarations(t *testing.T) {
	libc := &Node{Dep: pom.Dependency{GroupID: "t", ArtifactID: "libc", Version: "1.0"}}
	liba := &Node{Dep: pom.Dependency{GroupID: "t", ArtifactID: "liba", Version: "1.0"}, Children: []*Node{libc},
		eff: &Effective{Deps: []pom.Dependency{{GroupID: "t", ArtifactID: "libc", Version: "1.0"}}}}
	// libb also asks for libc, but wants 2.0; nearest-wins dropped it from the tree
	libb := &Node{Dep: pom.Dependency{GroupID: "t", ArtifactID: "libb", Version: "1.0"},
		eff: &Effective{Deps: []pom.Dependency{{GroupID: "t", ArtifactID: "other", Version: "9"}, {GroupID: "t", ArtifactID: "libc", Version: "2.0"}}}}
	roots := []*Node{liba, libb}

	got := Requirers(roots, "t:libc")
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].By != "t:liba:1.0" || got[0].Wants != "1.0" || !got[0].OnPath {
		t.Errorf("liba = %+v", got[0])
	}
	if got[1].By != "t:libb:1.0" || got[1].Wants != "2.0" || got[1].OnPath {
		t.Errorf("libb (mediated away) = %+v", got[1])
	}
	if len(Requirers(roots, "nothing")) != 0 {
		t.Error("no match, no requirers")
	}
}
