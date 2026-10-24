// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package ktsparse

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseKTS(t *testing.T) {
	dir := t.TempDir()
	ktsFile := filepath.Join(dir, "build.gradle.kts")

	content := `
plugins {
    java
    id("org.springframework.boot") version "3.2.2"
}

group = "com.example"
version = "1.0"

repositories {
    mavenCentral()
}

dependencies {
    implementation("com.google.guava:guava:31.1-jre")
    testImplementation("org.junit.jupiter:junit-jupiter:5.10.2")
    api(project(":core"))
}
`
	if err := os.WriteFile(ktsFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Parse(ktsFile)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if m.Group != "com.example" || m.Version != "1.0" {
		t.Errorf("unexpected coords: %s:%s", m.Group, m.Version)
	}
	if len(m.Plugins) != 2 {
		t.Errorf("expected 2 plugins, got %d", len(m.Plugins))
	}
	if len(m.Dependencies) != 3 {
		t.Errorf("expected 3 deps, got %d", len(m.Dependencies))
	}

	// Test ErrUnsupported on custom task
	taskContent := content + "\ntasks.register(\"hello\") {}\n"
	taskFile := filepath.Join(dir, "custom.gradle.kts")
	if err := os.WriteFile(taskFile, []byte(taskContent), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Parse(taskFile)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for custom task, got: %v", err)
	}
}
