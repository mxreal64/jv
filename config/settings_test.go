// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package config

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestMirrorMatches(t *testing.T) {
	tests := []struct {
		spec, id string
		want     bool
	}{
		{"*", "central", true},
		{"external:*", "repo", true},
		{"external:http:*", "repo", true},
		{"central", "central", true},
		{"central", "other", false},
		{"*,!internal", "central", true},
		{"*,!internal", "internal", false},
		{"!internal,*", "internal", false},
		{"repo1,repo2", "repo2", true},
		{"repo1,repo2", "repo3", false},
	}
	for _, tt := range tests {
		got := matches(tt.spec, tt.id)
		if got != tt.want {
			t.Errorf("matches(%q, %q) = %v; want %v", tt.spec, tt.id, got, tt.want)
		}
	}
}

func TestLoadSettingsCustomPath(t *testing.T) {
	tmp := t.TempDir()
	xml := `<settings>
	<localRepository>/custom/repo</localRepository>
	<activeProfiles>
	<activeProfile>test-prof</activeProfile>
	</activeProfiles>
	<servers>
	<server>
	<id>my-server</id>
	<username>user1</username>
	<password>secret</password>
	</server>
	</servers>
	</settings>`
	p := filepath.Join(tmp, "settings.xml")
	if err := os.WriteFile(p, []byte(xml), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := LoadSettings(p)
	if err != nil {
		t.Fatalf("LoadSettings failed: %v", err)
	}
	if s.LocalRepository != "/custom/repo" {
		t.Errorf("LocalRepository = %q; want %q", s.LocalRepository, "/custom/repo")
	}
	if len(s.ActiveProfiles) != 1 || s.ActiveProfiles[0] != "test-prof" {
		t.Errorf("ActiveProfiles = %v; want [test-prof]", s.ActiveProfiles)
	}
	u, pw := s.Auth("my-server")
	if u != "user1" || pw != "secret" {
		t.Errorf("Auth(my-server) = (%q, %q); want (user1, secret)", u, pw)
	}
}

func TestSettingsProfilesAndProxies(t *testing.T) {
	const x = `<settings><activeProfiles><activeProfile>corp</activeProfile></activeProfiles>
<profiles>
 <profile><id>corp</id><properties><nexus.url>https://n.example/repo</nexus.url></properties>
  <repositories><repository><id>corp-repo</id><url>https://n.example/repo</url></repository></repositories></profile>
 <profile><id>dflt</id><activation><activeByDefault>true</activeByDefault></activation><properties><d>1</d></properties></profile>
 <profile><id>off</id><properties><o>1</o></properties><repositories><repository><id>x</id><url>https://x.example</url></repository></repositories></profile>
</profiles>
<proxies>
 <proxy><id>p1</id><active>true</active><protocol>http</protocol><host>proxy.example</host><port>8080</port><username>u</username><password>pw</password><nonProxyHosts>localhost|*.internal</nonProxyHosts></proxy>
 <proxy><id>p2</id><active>false</active><protocol>https</protocol><host>nope</host><port>1</port></proxy>
</proxies></settings>`
	path := filepath.Join(t.TempDir(), "settings.xml")
	if err := os.WriteFile(path, []byte(x), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{"corp": true}
	if r := s.ActiveRepos(ids); len(r) != 1 || r[0].ID != "corp-repo" || r[0].URL != "https://n.example/repo" {
		t.Fatalf("repos: %+v", r)
	}
	props := s.ActiveProps(ids)
	if props["nexus.url"] != "https://n.example/repo" || props["d"] != "1" || props["o"] != "" {
		t.Fatalf("props: %v", props)
	}
	if r := s.ActiveRepos(map[string]bool{"corp": true, "off": true}); len(r) != 2 {
		t.Fatalf("-P off should add its repository: %+v", r)
	}
	if r := s.ActiveRepos(map[string]bool{"!corp": true}); len(r) != 0 {
		t.Fatalf("!corp should deactivate it: %+v", r)
	}

	pf := s.ProxyFunc()
	if pf == nil {
		t.Fatal("expected a proxy function")
	}
	req, _ := http.NewRequest("GET", "https://repo1.maven.org/maven2/x", nil)
	u, err := pf(req)
	if err != nil || u == nil || u.Host != "proxy.example:8080" || u.User.Username() != "u" {
		t.Fatalf("proxy for central: %v %v", u, err)
	}
	for _, host := range []string{"https://localhost/x", "https://a.internal/x"} {
		req, _ = http.NewRequest("GET", host, nil)
		if u, _ := pf(req); u != nil {
			t.Fatalf("%s must bypass the proxy, got %v", host, u)
		}
	}
	if (&Settings{}).ProxyFunc() != nil {
		t.Fatal("no <proxy> must leave the environment proxy in charge")
	}
}
