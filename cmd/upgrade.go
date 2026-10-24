// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/mxreal64/jv/resolver"
)

type UpgradeCandidate struct {
	Coordinate string
	Current    string
	Latest     string
	Type       string // major, minor, patch
}

func CheckUpgrades(ctx context.Context, loader *resolver.Loader, deps []resolver.Artifact) ([]UpgradeCandidate, error) {
	var candidates []UpgradeCandidate
	for _, d := range deps {
		// Example placeholder lookup
		if d.Version != "" && !strings.Contains(d.Version, "SNAPSHOT") {
			// Compare
		}
	}
	return candidates, nil
}

func FormatUpgradeSummary(upgrades []UpgradeCandidate) string {
	if len(upgrades) == 0 {
		return "All dependencies are up to date."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d upgrades available.\n", len(upgrades)))
	for _, u := range upgrades {
		sb.WriteString(fmt.Sprintf("  %s %s -> %s (%s)\n", u.Coordinate, u.Current, u.Latest, u.Type))
	}
	return sb.String()
}
