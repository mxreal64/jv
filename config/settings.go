// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package config

import (
	"bytes"
	"encoding/xml"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Server struct {
	ID       string `xml:"id"`
	Username string `xml:"username"`
	Password string `xml:"password"`
}

type Mirror struct {
	ID       string `xml:"id"`
	URL      string `xml:"url"`
	MirrorOf string `xml:"mirrorOf"`
}

// Proxy is a settings.xml <proxy>.
type Proxy struct {
	ID            string `xml:"id"`
	Active        string `xml:"active"`
	Protocol      string `xml:"protocol"`
	Host          string `xml:"host"`
	Port          string `xml:"port"`
	Username      string `xml:"username"`
	Password      string `xml:"password"`
	NonProxyHosts string `xml:"nonProxyHosts"`
}

// Repo is a repository declared by a settings.xml profile.
type Repo struct {
	ID  string `xml:"id"`
	URL string `xml:"url"`
}

type propItem struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

// SettingsProfile is a settings.xml <profile>; only repositories and properties matter to jv.
type SettingsProfile struct {
	ID         string `xml:"id"`
	Activation struct {
		ActiveByDefault string `xml:"activeByDefault"`
	} `xml:"activation"`
	Properties struct {
		Items []propItem `xml:",any"`
	} `xml:"properties"`
	Repositories []Repo `xml:"repositories>repository"`
}

type Settings struct {
	LocalRepository string            `xml:"localRepository"`
	Servers         []Server          `xml:"servers>server"`
	Mirrors         []Mirror          `xml:"mirrors>mirror"`
	Proxies         []Proxy           `xml:"proxies>proxy"`
	Profiles        []SettingsProfile `xml:"profiles>profile"`
	ActiveProfiles  []string          `xml:"activeProfiles>activeProfile"`
}

var envRe = regexp.MustCompile(`\$\{env\.([^}]+)\}`)

func expand(s string) string {
	s = strings.TrimSpace(s)
	s = envRe.ReplaceAllStringFunc(s, func(m string) string { return os.Getenv(m[6 : len(m)-1]) })
	if h, err := os.UserHomeDir(); err == nil {
		s = strings.ReplaceAll(s, "${user.home}", h)
	}
	return s
}

// LoadSettings reads the given path or ~/.m2/settings.xml. A missing file yields empty settings.
func LoadSettings(customPath ...string) (*Settings, error) {
	var path string
	if len(customPath) > 0 && customPath[0] != "" {
		path = customPath[0]
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return &Settings{}, nil
		}
		path = filepath.Join(home, ".m2", "settings.xml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Settings{}, nil
		}
		return &Settings{}, err
	}
	var s Settings
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if err := dec.Decode(&s); err != nil {
		return &Settings{}, err
	}
	s.LocalRepository = expand(s.LocalRepository)
	for i := range s.Servers {
		s.Servers[i].ID = expand(s.Servers[i].ID)
		s.Servers[i].Username = expand(s.Servers[i].Username)
		s.Servers[i].Password = expand(s.Servers[i].Password)
	}
	for i := range s.Mirrors {
		s.Mirrors[i].ID = expand(s.Mirrors[i].ID)
		s.Mirrors[i].URL = expand(s.Mirrors[i].URL)
		s.Mirrors[i].MirrorOf = expand(s.Mirrors[i].MirrorOf)
	}
	for i := range s.ActiveProfiles {
		s.ActiveProfiles[i] = expand(s.ActiveProfiles[i])
	}
	for i := range s.Proxies {
		p := &s.Proxies[i]
		p.Host, p.Port = expand(p.Host), expand(p.Port)
		p.Username, p.Password = expand(p.Username), expand(p.Password)
		p.Protocol, p.Active = strings.TrimSpace(p.Protocol), strings.TrimSpace(p.Active)
		p.NonProxyHosts = strings.TrimSpace(p.NonProxyHosts)
	}
	for i := range s.Profiles {
		s.Profiles[i].ID = strings.TrimSpace(s.Profiles[i].ID)
		for j := range s.Profiles[i].Repositories {
			r := &s.Profiles[i].Repositories[j]
			r.ID, r.URL = expand(r.ID), expand(r.URL)
		}
	}
	return &s, nil
}

// RepoDir: $JV_REPO, then <localRepository>, then ~/.m2/repository.
func (s *Settings) RepoDir() string {
	var p string
	switch {
	case os.Getenv("JV_REPO") != "":
		p = os.Getenv("JV_REPO")
	case s.LocalRepository != "":
		p = s.LocalRepository
	default:
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, ".m2", "repository")
	}
	// jv changes directory per module, so a relative path must be pinned now.
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// MirrorFor returns the first mirror whose mirrorOf covers repoID
// (supports *, external:*, external:http:*, ids, and !id negations).
func (s *Settings) MirrorFor(repoID string) (Mirror, bool) {
	for _, m := range s.Mirrors {
		if m.URL != "" && matches(m.MirrorOf, repoID) {
			return m, true
		}
	}
	return Mirror{}, false
}

func matches(spec, id string) bool {
	ok := false
	for _, t := range strings.Split(spec, ",") {
		t = strings.TrimSpace(t)
		switch {
		case t == "!"+id:
			return false
		case t == "*" || t == "external:*" || t == "external:http:*" || t == id:
			ok = true
		}
	}
	return ok
}

func (s *Settings) Auth(serverID string) (string, string) {
	for _, srv := range s.Servers {
		if srv.ID == serverID {
			return srv.Username, srv.Password
		}
	}
	return "", ""
}

// IsEncrypted reports whether a settings.xml password is a Maven-encrypted {…} blob.
func IsEncrypted(p string) bool {
	return len(p) > 2 && strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}")
}

// profilesFor returns the settings profiles that are active: listed in <activeProfiles>,
// selected with -P (ids), or activeByDefault; "!id" in ids switches one off.
func (s *Settings) profilesFor(ids map[string]bool) []SettingsProfile {
	listed := map[string]bool{}
	for _, id := range s.ActiveProfiles {
		listed[strings.TrimSpace(id)] = true
	}
	var out []SettingsProfile
	for _, p := range s.Profiles {
		if ids["!"+p.ID] {
			continue
		}
		if listed[p.ID] || ids[p.ID] || strings.TrimSpace(p.Activation.ActiveByDefault) == "true" {
			out = append(out, p)
		}
	}
	return out
}

// ActiveRepos returns the repositories declared by active settings.xml profiles.
func (s *Settings) ActiveRepos(ids map[string]bool) []Repo {
	var out []Repo
	for _, p := range s.profilesFor(ids) {
		out = append(out, p.Repositories...)
	}
	return out
}

// ActiveProps returns the properties declared by active settings.xml profiles.
func (s *Settings) ActiveProps(ids map[string]bool) map[string]string {
	out := map[string]string{}
	for _, p := range s.profilesFor(ids) {
		for _, it := range p.Properties.Items {
			out[it.XMLName.Local] = strings.TrimSpace(it.Value)
		}
	}
	return out
}

// hostExcluded implements <nonProxyHosts>: '|'-separated host names with '*' wildcards.
func hostExcluded(host, patterns string) bool {
	host = strings.ToLower(host)
	for _, p := range strings.FieldsFunc(patterns, func(r rune) bool { return r == '|' || r == ',' }) {
		p = strings.ToLower(strings.TrimSpace(p))
		switch {
		case p == "":
		case p == "*":
			return true
		case len(p) > 2 && strings.HasPrefix(p, "*") && strings.HasSuffix(p, "*"):
			if strings.Contains(host, p[1:len(p)-1]) {
				return true
			}
		case strings.HasPrefix(p, "*"):
			if strings.HasSuffix(host, p[1:]) {
				return true
			}
		case strings.HasSuffix(p, "*"):
			if strings.HasPrefix(host, p[:len(p)-1]) {
				return true
			}
		case host == p:
			return true
		}
	}
	return false
}

// ProxyFunc builds an http.Transport proxy selector from the active <proxy> entries, or
// returns nil when settings.xml configures none (the environment's HTTP(S)_PROXY then applies).
func (s *Settings) ProxyFunc() func(*http.Request) (*url.URL, error) {
	var active []Proxy
	for _, p := range s.Proxies {
		if p.Active != "false" && p.Host != "" {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func(req *http.Request) (*url.URL, error) {
		chosen := active[0]
		for _, p := range active {
			if strings.EqualFold(p.Protocol, req.URL.Scheme) {
				chosen = p
				break
			}
		}
		if hostExcluded(req.URL.Hostname(), chosen.NonProxyHosts) {
			return nil, nil
		}
		host := chosen.Host
		if chosen.Port != "" {
			host += ":" + chosen.Port
		}
		u := &url.URL{Scheme: "http", Host: host}
		if chosen.Username != "" && !IsEncrypted(chosen.Password) {
			u.User = url.UserPassword(chosen.Username, chosen.Password)
		}
		return u, nil
	}
}
