// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitProject(t *testing.T) {
	dir := t.TempDir()
	if err := InitProject(dir, "com.mycorp", "testapp", "1.0.0", "standard"); err != nil {
		t.Fatalf("InitProject failed: %v", err)
	}

	pomPath := filepath.Join(dir, "pom.xml")
	if _, err := os.Stat(pomPath); err != nil {
		t.Errorf("pom.xml missing")
	}

	srcFile := filepath.Join(dir, "src/main/java/com/mycorp/App.java")
	if _, err := os.Stat(srcFile); err != nil {
		t.Errorf("App.java missing at %s", srcFile)
	}

	testFile := filepath.Join(dir, "src/test/java/com/mycorp/AppTest.java")
	if _, err := os.Stat(testFile); err != nil {
		t.Errorf("AppTest.java missing at %s", testFile)
	}
}
