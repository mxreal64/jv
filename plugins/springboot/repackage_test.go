// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package springboot

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestJar(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRepackageLayout(t *testing.T) {
	dir := t.TempDir()
	origJar := filepath.Join(dir, "app-1.0.jar")
	loaderJar := filepath.Join(dir, "spring-boot-loader-3.2.0.jar")
	depJar := filepath.Join(dir, "dep-2.0.jar")
	outJar := filepath.Join(dir, "app-1.0-exec.jar")

	createTestJar(t, origJar, map[string]string{
		"com/example/App.class": "fake-bytecode-app",
		"application.yml":       "server.port: 8080",
	})

	createTestJar(t, loaderJar, map[string]string{
		"org/springframework/boot/loader/launch/JarLauncher.class": "fake-launcher-code",
		"org/springframework/boot/loader/archive/Archive.class":    "fake-archive-code",
	})

	createTestJar(t, depJar, map[string]string{
		"org/lib/Lib.class": "fake-lib-code",
	})

	runtimeCP := []string{loaderJar, depJar}
	mainClass := "com.example.App"
	version := "3.2.0"

	if err := Repackage(origJar, runtimeCP, mainClass, version, outJar); err != nil {
		t.Fatalf("Repackage failed: %v", err)
	}

	zr, err := zip.OpenReader(outJar)
	if err != nil {
		t.Fatalf("reading output jar: %v", err)
	}
	defer zr.Close()

	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[f.Name] = f
	}

	if _, ok := entries["BOOT-INF/classes/com/example/App.class"]; !ok {
		t.Errorf("missing BOOT-INF/classes/com/example/App.class")
	}
	if _, ok := entries["BOOT-INF/classes/application.yml"]; !ok {
		t.Errorf("missing BOOT-INF/classes/application.yml")
	}
	if _, ok := entries["BOOT-INF/lib/dep-2.0.jar"]; !ok {
		t.Errorf("missing BOOT-INF/lib/dep-2.0.jar")
	}
	if _, ok := entries["BOOT-INF/classpath.idx"]; !ok {
		t.Errorf("missing BOOT-INF/classpath.idx")
	}
	if _, ok := entries["BOOT-INF/layers.idx"]; !ok {
		t.Errorf("missing BOOT-INF/layers.idx")
	}
	if _, ok := entries["org/springframework/boot/loader/launch/JarLauncher.class"]; !ok {
		t.Errorf("missing org/springframework/boot/loader/launch/JarLauncher.class at root")
	}

	mfFile := entries["META-INF/MANIFEST.MF"]
	if mfFile == nil {
		t.Fatalf("missing META-INF/MANIFEST.MF")
	}
	rc, _ := mfFile.Open()
	mfBytes, _ := io.ReadAll(rc)
	rc.Close()
	mfStr := string(mfBytes)

	if !strings.Contains(mfStr, "Main-Class: org.springframework.boot.loader.launch.JarLauncher") {
		t.Errorf("wrong Main-Class in manifest:\n%s", mfStr)
	}
	if !strings.Contains(mfStr, "Start-Class: com.example.App") {
		t.Errorf("wrong Start-Class in manifest:\n%s", mfStr)
	}
}
