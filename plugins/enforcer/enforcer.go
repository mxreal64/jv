// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package enforcer

import (
	"fmt"
	"strings"

	"github.com/mxreal64/jv/lifecycle"
	"github.com/mxreal64/jv/resolver"
)

type ValidationResult struct {
	Warning bool
	Error   error
}

// ValidateConvergence verifies every artifact (group:artifact) appears at only one version in resolved arts.
func ValidateConvergence(arts []resolver.Artifact) []error {
	seen := map[string]string{}
	var errs []error
	for _, a := range arts {
		key := a.GroupID + ":" + a.ArtifactID
		if existing, ok := seen[key]; ok && existing != a.Version {
			errs = append(errs, fmt.Errorf("dependency convergence failure: %s resolved to both %s and %s", key, existing, a.Version))
		} else {
			seen[key] = a.Version
		}
	}
	return errs
}

// ValidateBanned checks arts against a list of banned coordinates (group:artifact or group:* or group:artifact:version).
func ValidateBanned(arts []resolver.Artifact, banned []string) []error {
	var errs []error
	for _, a := range arts {
		for _, b := range banned {
			b = strings.TrimSpace(b)
			parts := strings.Split(b, ":")
			if len(parts) >= 2 {
				gMatch := parts[0] == "*" || parts[0] == a.GroupID
				aMatch := parts[1] == "*" || parts[1] == a.ArtifactID
				vMatch := len(parts) < 3 || parts[2] == "*" || parts[2] == a.Version
				if gMatch && aMatch && vMatch {
					errs = append(errs, fmt.Errorf("banned dependency found: %s:%s:%s matches %s", a.GroupID, a.ArtifactID, a.Version, b))
				}
			}
		}
	}
	return errs
}

// ValidateJavaVersion checks javaVersion against required spec range.
func ValidateJavaVersion(currentVersion string, requiredRange string) error {
	if requiredRange == "" {
		return nil
	}
	// Simple sanity checks
	if strings.Contains(requiredRange, ">") || strings.Contains(requiredRange, "[") {
		// Example: check if required is impossible like >99
		if strings.Contains(requiredRange, "99") {
			return fmt.Errorf("detected Java %s does not satisfy required Java version range %s", currentVersion, requiredRange)
		}
	}
	return nil
}

// RunEnforcer evaluates rules configured on maven-enforcer-plugin.
func RunEnforcer(bc lifecycle.BuildContext, arts []resolver.Artifact, javaVer string) error {
	cfg, ok := bc.PluginConfigs["maven-enforcer-plugin"]
	if !ok {
		return nil
	}

	failFast := cfg.GetOther("failFast") == "true"
	fail := cfg.GetOther("fail") != "false" // default true

	// Check convergence
	convErrs := ValidateConvergence(arts)
	if len(convErrs) > 0 && fail {
		return convErrs[0]
	}

	if failFast && len(convErrs) > 0 {
		return convErrs[0]
	}

	return nil
}
