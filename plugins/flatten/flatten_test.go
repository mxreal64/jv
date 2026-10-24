// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package flatten

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlattenResolveCiFriendly(t *testing.T) {
	dir := t.TempDir()
	pomFile := filepath.Join(dir, "pom.xml")
	outFile := filepath.Join(dir, ".flattened-pom.xml")

	pomContent := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>my-app</artifactId>
  <version>${revision}</version>
  <properties>
    <revision>1.0.0-SNAPSHOT</revision>
  </properties>
  <dependencies>
    <dependency>
      <groupId>com.example</groupId>
      <artifactId>my-dep</artifactId>
      <version>${revision}</version>
    </dependency>
  </dependencies>
  <build>
    <plugins>
      <plugin>
        <artifactId>flatten-maven-plugin</artifactId>
      </plugin>
    </plugins>
  </build>
</project>`

	if err := os.WriteFile(pomFile, []byte(pomContent), 0o644); err != nil {
		t.Fatal(err)
	}

	props := map[string]string{
		"revision": "1.0.0-SNAPSHOT",
	}

	if err := Flatten(pomFile, props, ModeResolveCiFriendlyVersions, outFile); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "${revision}") {
		t.Fatalf("expected ${revision} to be substituted, but found it in: %s", content)
	}
	if !strings.Contains(content, "<version>1.0.0-SNAPSHOT</version>") {
		t.Fatalf("expected resolved version, got: %s", content)
	}
	if !strings.Contains(content, "<build>") {
		t.Fatalf("expected <build> to be preserved in resolveCiFriendlyVersions mode")
	}
}

func TestFlattenOssrhMode(t *testing.T) {
	dir := t.TempDir()
	pomFile := filepath.Join(dir, "pom.xml")
	outFile := filepath.Join(dir, ".flattened-pom.xml")

	pomContent := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>my-app</artifactId>
  <version>${revision}</version>
  <build>
    <plugins></plugins>
  </build>
  <profiles>
    <profile></profile>
  </profiles>
</project>`

	if err := os.WriteFile(pomFile, []byte(pomContent), 0o644); err != nil {
		t.Fatal(err)
	}

	props := map[string]string{
		"revision": "2.0.0",
	}

	if err := Flatten(pomFile, props, ModeOssrh, outFile); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "<build>") {
		t.Fatalf("expected <build> to be stripped in ossrh mode")
	}
	if strings.Contains(content, "<profiles>") {
		t.Fatalf("expected <profiles> to be stripped in ossrh mode")
	}
	if !strings.Contains(content, "<version>2.0.0</version>") {
		t.Fatalf("expected <version>2.0.0</version>, got: %s", content)
	}
}

func TestFlattenDefaultsModeAndEdgeCases(t *testing.T) {
	dir := t.TempDir()
	pomFile := filepath.Join(dir, "pom.xml")
	outFile := filepath.Join(dir, ".flattened-pom.xml")

	pomContent := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>my-app</artifactId>
  <version>1.0.0</version>
  <repositories><repository></repository></repositories>
  <pluginRepositories><pluginRepository></pluginRepository></pluginRepositories>
  <distributionManagement><status></status></distributionManagement>
  <dependencyManagement><dependencies></dependencies></dependencyManagement>
  <build><plugins></plugins></build>
  <profiles><profile></profile></profiles>
</project>`

	if err := os.WriteFile(pomFile, []byte(pomContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test ModeDefaults
	if err := Flatten(pomFile, nil, ModeDefaults, outFile); err != nil {
		t.Fatalf("Flatten with ModeDefaults failed: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "<dependencyManagement>") || strings.Contains(content, "<repositories>") || strings.Contains(content, "<build>") {
		t.Fatalf("expected sections stripped in ModeDefaults, got: %s", content)
	}

	// Test empty mode defaults to ModeResolveCiFriendlyVersions
	if err := Flatten(pomFile, nil, "", outFile); err != nil {
		t.Fatalf("Flatten with empty mode failed: %v", err)
	}
	data, _ = os.ReadFile(outFile)
	if !strings.Contains(string(data), "<dependencyManagement>") {
		t.Fatalf("expected dependencyManagement preserved in default empty mode")
	}

	// Test nonexistent file error
	if err := Flatten(filepath.Join(dir, "missing.xml"), nil, ModeDefaults, outFile); err == nil {
		t.Fatalf("expected error for missing pom file")
	}
}
