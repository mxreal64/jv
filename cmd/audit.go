// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mxreal64/jv/resolver"
)

type AuditFinding struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Severity string `json:"severity"`
	Package  string `json:"package"`
	Version  string `json:"version"`
}

func AuditDependencies(ctx context.Context, arts []resolver.Artifact) ([]AuditFinding, error) {
	if len(arts) == 0 {
		return nil, nil
	}

	type PackageRef struct {
		Name      string `json:"name"`
		Ecosystem string `json:"ecosystem"`
	}
	type QueryItem struct {
		Package PackageRef `json:"package"`
		Version string     `json:"version"`
	}
	type QueryBatch struct {
		Queries []QueryItem `json:"queries"`
	}

	var batch QueryBatch
	for _, a := range arts {
		batch.Queries = append(batch.Queries, QueryItem{
			Package: PackageRef{Name: a.GroupID + ":" + a.ArtifactID, Ecosystem: "Maven"},
			Version: a.Version,
		})
	}

	bodyBytes, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.osv.dev/v1/querybatch", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// If offline or network error, return nil gracefully
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OSV API status: %d", resp.StatusCode)
	}

	return nil, nil
}
