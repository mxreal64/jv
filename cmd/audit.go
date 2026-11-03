// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mxreal64/jv/resolver"
)

// AuditFinding is one known vulnerability affecting one resolved dependency.
type AuditFinding struct {
	ID       string  `json:"id"`
	Summary  string  `json:"summary"`
	Severity string  `json:"severity"` // CRITICAL, HIGH, MEDIUM, LOW or UNKNOWN
	Score    float64 `json:"score,omitempty"`
	Package  string  `json:"package"` // groupId:artifactId
	Version  string  `json:"version"`
}

const osvURL = "https://api.osv.dev"

// Auditor queries an OSV (osv.dev) compatible API.
type Auditor struct {
	BaseURL string       // default https://api.osv.dev, or $JV_OSV_URL (for a mirror)
	Client  *http.Client // default: 30s timeout
}

// AuditDependencies looks every artifact up in the OSV database. Network and API errors are
// returned, never hidden: an audit that could not run must not look like a clean one.
func AuditDependencies(ctx context.Context, arts []resolver.Artifact) ([]AuditFinding, error) {
	return (&Auditor{}).Audit(ctx, arts)
}

type osvPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

type osvQuery struct {
	Package   osvPackage `json:"package"`
	Version   string     `json:"version"`
	PageToken string     `json:"page_token,omitempty"`
}

type osvBatchResult struct {
	Vulns []struct {
		ID string `json:"id"`
	} `json:"vulns"`
	NextPageToken string `json:"next_page_token"`
}

type osvVuln struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Details  string `json:"details"`
	Severity []struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	} `json:"severity"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
}

func (a *Auditor) base() string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	if u := os.Getenv("JV_OSV_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return osvURL
}

func (a *Auditor) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (a *Auditor) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "jv")
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OSV API %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type pkgVersion struct{ name, version string }

// Audit returns the findings for arts, most severe first.
func (a *Auditor) Audit(ctx context.Context, arts []resolver.Artifact) ([]AuditFinding, error) {
	var pkgs []pkgVersion
	seen := map[pkgVersion]bool{}
	for _, art := range arts {
		pv := pkgVersion{art.GroupID + ":" + art.ArtifactID, art.Version}
		if art.Version == "" || strings.HasSuffix(art.Version, "-SNAPSHOT") || seen[pv] {
			continue // OSV has no data for unversioned or snapshot artifacts
		}
		seen[pv] = true
		pkgs = append(pkgs, pv)
	}
	if len(pkgs) == 0 {
		return nil, nil
	}

	ids := make(map[pkgVersion][]string, len(pkgs))
	const chunk = 1000 // OSV's batch limit
	for start := 0; start < len(pkgs); start += chunk {
		end := start + chunk
		if end > len(pkgs) {
			end = len(pkgs)
		}
		part := pkgs[start:end]
		req := struct {
			Queries []osvQuery `json:"queries"`
		}{}
		for _, p := range part {
			req.Queries = append(req.Queries, osvQuery{Package: osvPackage{Name: p.name, Ecosystem: "Maven"}, Version: p.version})
		}
		var resp struct {
			Results []osvBatchResult `json:"results"`
		}
		if err := a.do(ctx, "POST", "/v1/querybatch", req, &resp); err != nil {
			return nil, fmt.Errorf("querying osv.dev: %w", err)
		}
		if len(resp.Results) != len(part) {
			return nil, fmt.Errorf("osv.dev returned %d results for %d queries", len(resp.Results), len(part))
		}
		for i, res := range resp.Results {
			p := part[i]
			for _, v := range res.Vulns {
				ids[p] = append(ids[p], v.ID)
			}
			for token := res.NextPageToken; token != ""; { // long result lists are paged
				q := osvQuery{Package: osvPackage{Name: p.name, Ecosystem: "Maven"}, Version: p.version, PageToken: token}
				var page struct {
					Results []osvBatchResult `json:"results"`
				}
				body := struct {
					Queries []osvQuery `json:"queries"`
				}{[]osvQuery{q}}
				if err := a.do(ctx, "POST", "/v1/querybatch", body, &page); err != nil {
					return nil, fmt.Errorf("querying osv.dev: %w", err)
				}
				token = ""
				if len(page.Results) == 1 {
					for _, v := range page.Results[0].Vulns {
						ids[p] = append(ids[p], v.ID)
					}
					token = page.Results[0].NextPageToken
				}
			}
		}
	}

	// advisory id -> details (the batch endpoint only returns ids)
	unique := map[string]bool{}
	var order []string
	for _, list := range ids {
		for _, id := range list {
			if !unique[id] {
				unique[id] = true
				order = append(order, id)
			}
		}
	}
	details := make(map[string]osvVuln, len(order))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var firstErr error
	for _, id := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			var v osvVuln
			err := a.do(ctx, "GET", "/v1/vulns/"+id, nil, &v)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("fetching advisory %s: %w", id, err)
				}
				return
			}
			details[id] = v
		}(id)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	var out []AuditFinding
	for p, list := range ids {
		have := map[string]bool{}
		for _, id := range list {
			if have[id] {
				continue
			}
			have[id] = true
			d := details[id]
			sev, score := d.severity()
			out = append(out, AuditFinding{ID: id, Summary: d.summary(), Severity: sev, Score: score, Package: p.name, Version: p.version})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := SeverityRank(out[i].Severity), SeverityRank(out[j].Severity); ri != rj {
			return ri > rj
		}
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (v osvVuln) summary() string {
	if s := strings.TrimSpace(v.Summary); s != "" {
		return s
	}
	d := strings.TrimSpace(v.Details)
	if i := strings.IndexByte(d, '\n'); i >= 0 {
		d = d[:i]
	}
	if len(d) > 120 {
		d = d[:120] + "..."
	}
	return d
}

// severity prefers the severity label of the advisory database (GitHub advisories carry one), then
// falls back to the CVSS v3 base score computed from the vector.
func (v osvVuln) severity() (string, float64) {
	var score float64
	for _, s := range v.Severity {
		if strings.HasPrefix(s.Score, "CVSS:3") {
			if sc, ok := cvss3Score(s.Score); ok {
				score = sc
				break
			}
		}
	}
	switch label := strings.ToUpper(strings.TrimSpace(v.DatabaseSpecific.Severity)); label {
	case "CRITICAL", "HIGH", "LOW":
		return label, score
	case "MODERATE", "MEDIUM":
		return "MEDIUM", score
	}
	if score > 0 {
		return severityFromScore(score), score
	}
	return "UNKNOWN", 0
}

func severityFromScore(s float64) string {
	switch {
	case s >= 9.0:
		return "CRITICAL"
	case s >= 7.0:
		return "HIGH"
	case s >= 4.0:
		return "MEDIUM"
	case s > 0:
		return "LOW"
	}
	return "UNKNOWN"
}

// SeverityRank orders severities: CRITICAL 4 ... LOW 1, anything else 0.
func SeverityRank(s string) int {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	}
	return 0
}

// cvss3Score computes the CVSS v3.x base score of a vector such as
// "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H".
func cvss3Score(vector string) (float64, bool) {
	m := map[string]string{}
	for _, part := range strings.Split(vector, "/")[1:] {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	av, ok1 := map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}[m["AV"]]
	ac, ok2 := map[string]float64{"L": 0.77, "H": 0.44}[m["AC"]]
	ui, ok3 := map[string]float64{"N": 0.85, "R": 0.62}[m["UI"]]
	cia := map[string]float64{"H": 0.56, "L": 0.22, "N": 0}
	c, ok4 := cia[m["C"]]
	i, ok5 := cia[m["I"]]
	a, ok6 := cia[m["A"]]
	changed := m["S"] == "C"
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) || (m["S"] != "C" && m["S"] != "U") {
		return 0, false
	}
	var pr float64
	switch m["PR"] {
	case "N":
		pr = 0.85
	case "L":
		pr = 0.62
		if changed {
			pr = 0.68
		}
	case "H":
		pr = 0.27
		if changed {
			pr = 0.5
		}
	default:
		return 0, false
	}
	iss := 1 - (1-c)*(1-i)*(1-a)
	var impact float64
	if changed {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	} else {
		impact = 6.42 * iss
	}
	if impact <= 0 {
		return 0, true
	}
	expl := 8.22 * av * ac * pr * ui
	var base float64
	if changed {
		base = math.Min(1.08*(impact+expl), 10)
	} else {
		base = math.Min(impact+expl, 10)
	}
	return math.Ceil(base*10) / 10, true
}
