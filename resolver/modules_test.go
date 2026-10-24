// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func miniPOM(artifact string) string {
	return "<project><modelVersion>4.0.0</modelVersion><groupId>t</groupId><artifactId>" + artifact + "</artifactId><version>1</version></project>"
}

func TestListModulesCustomPOMNames(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "pom.xml", "<project><modelVersion>4.0.0</modelVersion><groupId>t</groupId><artifactId>root</artifactId><version>1</version><packaging>pom</packaging>"+
		"<modules><module>plain</module><module>sub/alt.xml</module></modules></project>")
	writeTree(t, root, "plain/pom.xml", miniPOM("plain"))
	writeTree(t, root, "sub/alt.xml", miniPOM("alt"))

	refs, err := ListModules(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []moduleRef{
		{Dir: root, POMFile: filepath.Join(root, "pom.xml")},
		{Dir: filepath.Join(root, "plain"), POMFile: filepath.Join(root, "plain", "pom.xml")},
		{Dir: filepath.Join(root, "sub"), POMFile: filepath.Join(root, "sub", "alt.xml")},
	}
	if len(refs) != len(want) {
		t.Fatalf("got %d modules, want %d: %+v", len(refs), len(want), refs)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("module %d = %+v, want %+v", i, refs[i], want[i])
		}
	}
}
