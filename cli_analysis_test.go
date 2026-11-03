// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pomXML(g, a, v string, deps ...string) string {
	var d strings.Builder
	for _, dep := range deps { // "g:a:v"
		p := strings.Split(dep, ":")
		fmt.Fprintf(&d, "<dependency><groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version></dependency>", p[0], p[1], p[2])
	}
	return fmt.Sprintf(`<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>
<groupId>%s</groupId><artifactId>%s</artifactId><version>%s</version><dependencies>%s</dependencies></project>`, g, a, v, d.String())
}

// fakeRepo builds an offline local repository: liba -> libc, libb -> libc, with published versions
// of liba in its (cached) metadata.
func fakeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	put := func(g, a, v, body string) {
		writeFile(t, filepath.Join(repo, filepath.FromSlash(strings.ReplaceAll(g, ".", "/")), a, v, a+"-"+v+".pom"), body)
	}
	put("org.acme", "libc", "1.0", pomXML("org.acme", "libc", "1.0"))
	put("org.acme", "liba", "1.0", pomXML("org.acme", "liba", "1.0", "org.acme:libc:1.0"))
	put("org.acme", "libb", "1.0", pomXML("org.acme", "libb", "1.0", "org.acme:libc:1.0"))
	writeFile(t, filepath.Join(repo, "org", "acme", "liba", "maven-metadata-jv.xml"),
		`<metadata><versioning><versions><version>1.0</version><version>1.1</version><version>2.0.0</version><version>3.0.0-beta1</version></versions></versioning></metadata>`)
	writeFile(t, filepath.Join(repo, "org", "acme", "libb", "maven-metadata-jv.xml"),
		`<metadata><versioning><versions><version>1.0</version></versions></versioning></metadata>`)
	return repo
}

func appProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pom.xml"), pomXML("demo", "app", "1.0", "org.acme:liba:1.0", "org.acme:libb:1.0"))
	return dir
}

// runIn runs jv in dir and returns its exit code and stdout.
func runIn(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	code := run(args)
	w.Close()
	os.Stdout = old
	return code, <-done
}

func TestExplainShowsEveryPath(t *testing.T) {
	t.Setenv("JV_REPO", fakeRepo(t))
	code, out := runIn(t, appProject(t), "-q", "-o", "explain", "libc")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"demo:app:1.0", "org.acme:liba:1.0 -> org.acme:libc:1.0", "declared by:",
		"org.acme:liba:1.0 wants 1.0", "org.acme:libb:1.0 wants 1.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if code, _ := runIn(t, appProject(t), "-q", "-o", "explain", "org.acme:libc:9.9"); code != 1 {
		t.Errorf("no match must exit 1, got %d", code)
	}
	if code, _ := runIn(t, appProject(t), "-q", "-o", "explain"); code != 2 {
		t.Errorf("explain without a query must exit 2, got %d", code)
	}
}

func TestUpgradeListsNewerReleases(t *testing.T) {
	t.Setenv("JV_REPO", fakeRepo(t))
	code, out := runIn(t, appProject(t), "-q", "-o", "upgrade")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "org.acme:liba 1.0 -> 2.0.0 (major)") {
		t.Errorf("liba should be offered 2.0.0 (not the 3.0.0-beta1 pre-release):\n%s", out)
	}
	if strings.Contains(out, "libb") || strings.Contains(out, "beta") {
		t.Errorf("libb is current and pre-releases must not be offered:\n%s", out)
	}
}

// osvBatch answers an OSV batch query with one result per query; packages containing vulnerable get
// an advisory.
func osvBatch(w http.ResponseWriter, r *http.Request, vulnerable string) {
	var req struct {
		Queries []struct {
			Package struct {
				Name string `json:"name"`
			} `json:"package"`
		} `json:"queries"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var results []string
	for _, q := range req.Queries {
		if vulnerable != "" && q.Package.Name == vulnerable {
			results = append(results, `{"vulns":[{"id":"GHSA-test-1"}]}`)
		} else {
			results = append(results, `{}`)
		}
	}
	fmt.Fprintf(w, `{"results":[%s]}`, strings.Join(results, ","))
}

func osvTestServer(t *testing.T, severity string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/querybatch" {
			osvBatch(w, r, "org.acme:libc")
			return
		}
		fmt.Fprintf(w, `{"id":"GHSA-test-1","summary":"libc is unsafe","database_specific":{"severity":%q}}`, severity)
	}))
}

func TestAuditExitCodes(t *testing.T) {
	t.Setenv("JV_REPO", fakeRepo(t))

	if code, _ := runIn(t, appProject(t), "-q", "-o", "audit"); code != 2 {
		t.Errorf("audit -o cannot work and must say so (exit 2), got %d", code)
	}

	srv := osvTestServer(t, "HIGH")
	defer srv.Close()
	t.Setenv("JV_OSV_URL", srv.URL)

	code, out := runIn(t, appProject(t), "-q", "audit")
	if code != 1 || !strings.Contains(out, "GHSA-test-1") || !strings.Contains(out, "org.acme:libc@1.0") {
		t.Errorf("a finding must fail the audit: exit %d\n%s", code, out)
	}
	if code, _ := runIn(t, appProject(t), "-q", "-Daudit.failOn=critical", "audit"); code != 0 {
		t.Errorf("a HIGH finding must pass -Daudit.failOn=critical, got %d", code)
	}
	if code, _ := runIn(t, appProject(t), "-q", "-Daudit.failOn=high", "audit"); code != 1 {
		t.Errorf("a HIGH finding must fail -Daudit.failOn=high, got %d", code)
	}
	if code, _ := runIn(t, appProject(t), "-q", "-Daudit.failOn=none", "audit"); code != 0 {
		t.Errorf("failOn=none must never fail, got %d", code)
	}
	if code, out := runIn(t, appProject(t), "-q", "-Daudit.json=true", "-Daudit.failOn=none", "audit"); code != 0 || !strings.Contains(out, `"id": "GHSA-test-1"`) {
		t.Errorf("json output: exit %d\n%s", code, out)
	}
	if code, _ := runIn(t, appProject(t), "-q", "-Daudit.failOn=bogus", "audit"); code != 2 {
		t.Errorf("an invalid failOn must exit 2, got %d", code)
	}

	// an unreachable OSV must never look like a clean audit
	srv.Close()
	if code, _ := runIn(t, appProject(t), "-q", "audit"); code != 2 {
		t.Errorf("unreachable OSV: want exit 2, got %d", code)
	}
}

func TestAuditCleanProject(t *testing.T) {
	t.Setenv("JV_REPO", fakeRepo(t))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		osvBatch(w, r, "")
	}))
	defer srv.Close()
	t.Setenv("JV_OSV_URL", srv.URL)
	code, out := runIn(t, appProject(t), "-q", "audit")
	if code != 0 || !strings.Contains(out, "No known vulnerabilities in 3 dependencies") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestInitCommand(t *testing.T) {
	dir := t.TempDir()
	code, _ := runIn(t, dir, "-q", "init", "-DgroupId=org.acme", "-DartifactId=hello", "-Dversion=2.0")
	if code != 0 {
		t.Fatalf("init exit %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "pom.xml"))
	if err != nil || !strings.Contains(string(b), "<artifactId>hello</artifactId>") || !strings.Contains(string(b), "<version>2.0</version>") {
		t.Fatalf("pom.xml: %v\n%s", err, b)
	}
	if code, _ := runIn(t, dir, "-q", "init"); code != 1 {
		t.Errorf("a second init must refuse (exit 1), got %d", code)
	}
	if b2, _ := os.ReadFile(filepath.Join(dir, "pom.xml")); string(b2) != string(b) {
		t.Error("the existing pom.xml was modified")
	}

	// the artifact id defaults to the (sanitised) directory name
	named := filepath.Join(t.TempDir(), "My Cool_App")
	if err := os.MkdirAll(named, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := runIn(t, named, "-q", "init"); code != 0 {
		t.Fatal("init failed")
	}
	if b, _ := os.ReadFile(filepath.Join(named, "pom.xml")); !strings.Contains(string(b), "<artifactId>my-cool_app</artifactId>") {
		t.Errorf("default artifactId:\n%s", b)
	}
}

func TestAnalysisGoalsNeedAMavenProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "build.gradle"), "// gradle")
	for _, g := range []string{"audit", "upgrade", "explain"} {
		if code, _ := runIn(t, dir, "-q", g, "x"); code != 2 {
			t.Errorf("%s in a Gradle project must exit 2, got %d", g, code)
		}
	}
}
