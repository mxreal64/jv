// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package enforcer

import (
	"testing"

	"github.com/mxreal64/jv/resolver"
)

func TestValidateConvergence(t *testing.T) {
	arts := []resolver.Artifact{
		{GroupID: "com.example", ArtifactID: "foo", Version: "1.0"},
		{GroupID: "com.example", ArtifactID: "foo", Version: "2.0"},
	}
	errs := ValidateConvergence(arts)
	if len(errs) == 0 {
		t.Fatalf("expected convergence failure")
	}

	okArts := []resolver.Artifact{
		{GroupID: "com.example", ArtifactID: "foo", Version: "1.0"},
		{GroupID: "com.example", ArtifactID: "bar", Version: "2.0"},
	}
	if errs2 := ValidateConvergence(okArts); len(errs2) > 0 {
		t.Fatalf("unexpected convergence failure: %v", errs2)
	}
}

func TestValidateBanned(t *testing.T) {
	arts := []resolver.Artifact{
		{GroupID: "commons-logging", ArtifactID: "commons-logging", Version: "1.2"},
	}
	banned := []string{"commons-logging:commons-logging"}
	errs := ValidateBanned(arts, banned)
	if len(errs) == 0 {
		t.Fatalf("expected banned dependency detection")
	}
}

func TestValidateJavaVersion(t *testing.T) {
	if err := ValidateJavaVersion("17.0.1", "[11,21]"); err != nil {
		t.Fatalf("unexpected failure: %v", err)
	}
	if err := ValidateJavaVersion("17.0.1", "[99,)"); err == nil {
		t.Fatalf("expected failure with impossible java range")
	}
}
