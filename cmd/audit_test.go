// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mxreal64/jv/resolver"
)

func TestCVSS3Score(t *testing.T) {
	cases := []struct {
		vec  string
		want float64
		sev  string
	}{
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", 9.8, "CRITICAL"},
		{"CVSS:3.1/AV:L/AC:H/PR:H/UI:R/S:U/C:L/I:N/A:N", 1.8, "LOW"},
		{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H", 10.0, "CRITICAL"},
		{"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N", 6.5, "MEDIUM"},
	}
	for _, c := range cases {
		got, ok := cvss3Score(c.vec)
		if !ok || math.Abs(got-c.want) > 0.05 {
			t.Errorf("%s: got %v ok=%v, want %v", c.vec, got, ok, c.want)
		}
		if s := severityFromScore(got); s != c.sev {
			t.Errorf("%s: severity %s, want %s", c.vec, s, c.sev)
		}
	}
	if _, ok := cvss3Score("CVSS:3.1/AV:X"); ok {
		t.Error("an incomplete vector must be rejected")
	}
}

func osvServer(t *testing.T, vulnsFor map[string][]string, details map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/querybatch":
			var req struct {
				Queries []osvQuery `json:"queries"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			var results []map[string]any
			for _, q := range req.Queries {
				if q.Package.Ecosystem != "Maven" {
					http.Error(w, "wrong ecosystem", 400)
					return
				}
				var vs []map[string]string
				for _, id := range vulnsFor[q.Package.Name+"@"+q.Version] {
					vs = append(vs, map[string]string{"id": id})
				}
				results = append(results, map[string]any{"vulns": vs})
			}
			json.NewEncoder(w).Encode(map[string]any{"results": results})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/vulns/"):
			if d, ok := details[strings.TrimPrefix(r.URL.Path, "/v1/vulns/")]; ok {
				fmt.Fprint(w, d)
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestAuditParsesFindingsAndSeverity(t *testing.T) {
	srv := osvServer(t,
		map[string][]string{
			"org.apache.logging.log4j:log4j-core@2.14.0": {"GHSA-jfh8", "CVE-2021-X"},
			"com.example:lib@1.0":                        {"GHSA-mod"},
		},
		map[string]string{
			"GHSA-jfh8":  `{"id":"GHSA-jfh8","summary":"Remote code execution","database_specific":{"severity":"CRITICAL"}}`,
			"CVE-2021-X": `{"id":"CVE-2021-X","details":"first line\nsecond","severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N"}]}`,
			"GHSA-mod":   `{"id":"GHSA-mod","summary":"Moderate thing","database_specific":{"severity":"MODERATE"}}`,
		})
	defer srv.Close()

	arts := []resolver.Artifact{
		{GroupID: "com.example", ArtifactID: "lib", Version: "1.0"},
		{GroupID: "org.apache.logging.log4j", ArtifactID: "log4j-core", Version: "2.14.0"},
		{GroupID: "org.apache.logging.log4j", ArtifactID: "log4j-core", Version: "2.14.0"}, // duplicate
		{GroupID: "clean", ArtifactID: "dep", Version: "1.0"},
		{GroupID: "snap", ArtifactID: "shot", Version: "1.0-SNAPSHOT"},
	}
	got, err := (&Auditor{BaseURL: srv.URL}).Audit(context.Background(), arts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d findings, want 3: %+v", len(got), got)
	}
	if got[0].ID != "GHSA-jfh8" || got[0].Severity != "CRITICAL" || got[0].Summary != "Remote code execution" {
		t.Errorf("first finding = %+v", got[0])
	}
	// equal severity: ordered by package name
	if got[1].ID != "GHSA-mod" || got[1].Severity != "MEDIUM" || got[1].Package != "com.example:lib" || got[1].Version != "1.0" {
		t.Errorf("second finding = %+v", got[1])
	}
	if got[2].ID != "CVE-2021-X" || got[2].Severity != "MEDIUM" || got[2].Summary != "first line" || got[2].Score < 6 {
		t.Errorf("third finding (CVSS fallback) = %+v", got[2])
	}
}

func TestAuditReportsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }))
	arts := []resolver.Artifact{{GroupID: "g", ArtifactID: "a", Version: "1"}}
	if _, err := (&Auditor{BaseURL: srv.URL}).Audit(context.Background(), arts); err == nil {
		t.Error("an API error must not look like a clean audit")
	}
	srv.Close()
	if _, err := (&Auditor{BaseURL: srv.URL}).Audit(context.Background(), arts); err == nil {
		t.Error("an unreachable API must not look like a clean audit")
	}
	if f, err := (&Auditor{BaseURL: srv.URL}).Audit(context.Background(), nil); f != nil || err != nil {
		t.Errorf("nothing to audit: %v %v", f, err)
	}
}

func TestAuditFollowsPagesAndHonoursMirrorEnv(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/querybatch" {
			var req struct {
				Queries []osvQuery `json:"queries"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Queries[0].PageToken == "" {
				fmt.Fprint(w, `{"results":[{"vulns":[{"id":"A-1"}],"next_page_token":"p2"}]}`)
			} else {
				pages++
				fmt.Fprint(w, `{"results":[{"vulns":[{"id":"A-2"}]}]}`)
			}
			return
		}
		fmt.Fprintf(w, `{"id":%q,"summary":"s","database_specific":{"severity":"HIGH"}}`, strings.TrimPrefix(r.URL.Path, "/v1/vulns/"))
	}))
	defer srv.Close()
	t.Setenv("JV_OSV_URL", srv.URL)
	got, err := AuditDependencies(context.Background(), []resolver.Artifact{{GroupID: "g", ArtifactID: "a", Version: "1"}})
	if err != nil || len(got) != 2 || pages != 1 {
		t.Fatalf("paging: findings=%v pages=%d err=%v", got, pages, err)
	}
}
