// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGradleProject(t *testing.T, dir, buildGradle, settingsGradle, mainJava string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src/main/java/demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.gradle"), []byte(buildGradle), 0o644); err != nil {
		t.Fatal(err)
	}
	if settingsGradle != "" {
		if err := os.WriteFile(filepath.Join(dir, "settings.gradle"), []byte(settingsGradle), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if mainJava != "" {
		if err := os.WriteFile(filepath.Join(dir, "src/main/java/demo/App.java"), []byte(mainJava), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParityGradle_SingleProject(t *testing.T) {
	requireTools(t, "gradle", "javac", "java")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	buildGradle := `plugins { id 'java' }
version = '1.0'
jar {
    manifest {
        attributes 'Main-Class': 'demo.App'
    }
}
`
	mainJava := `package demo;
public class App {
    public static String greet() { return "GRADLE_SINGLE_PARITY"; }
    public static void main(String[] args) {
        System.out.println(greet());
    }
}
`
	writeGradleProject(t, dir, buildGradle, "rootProject.name = 'singleapp'", mainJava)
	_ = os.MkdirAll(filepath.Join(dir, "src/main/resources"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src/main/resources/res.txt"), []byte("resource content"), 0o644)

	// Build with jv
	jvOut, err := runToolCmd(t, dir, nil, jvBin, "package")
	if err != nil {
		t.Fatalf("jv package failed: %v: %s", err, jvOut)
	}

	appClass := filepath.Join(dir, "build/classes/java/main/demo/App.class")
	if _, err := os.Stat(appClass); err != nil {
		t.Fatalf("expected compiled App.class: %v", err)
	}

	jarPath := filepath.Join(dir, "build/libs/singleapp-1.0.jar")
	javaOut, err := runToolCmd(t, dir, nil, "java", "-jar", jarPath)
	if err != nil || !strings.Contains(javaOut, "GRADLE_SINGLE_PARITY") {
		t.Fatalf("packaged jar failed: %v: %s", err, javaOut)
	}

	// Reference build with gradle to compare classes
	refDir := t.TempDir()
	writeGradleProject(t, refDir, buildGradle, "rootProject.name = 'singleapp'", mainJava)
	_, err = runToolCmd(t, refDir, nil, "gradle", "--no-daemon", "-q", "classes")
	if err != nil {
		t.Fatalf("gradle classes failed: %v", err)
	}

	refAppClass := filepath.Join(refDir, "build/classes/java/main/demo/App.class")
	if _, err := os.Stat(refAppClass); err != nil {
		t.Fatalf("gradle did not produce App.class: %v", err)
	}
}

func TestParityGradle_MultiProject(t *testing.T) {
	requireTools(t, "gradle", "javac", "java")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	settings := `rootProject.name = 'multiapp'
include 'core', 'app'
`
	if err := os.WriteFile(filepath.Join(dir, "settings.gradle"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	coreDir := filepath.Join(dir, "core")
	coreBuild := `plugins { id 'java-library' }
version = '1.0'
`
	coreJava := `package demo.core;
public class CoreUtil {
    public static String message() { return "GRADLE_MULTI_PARITY_SUCCESS"; }
}
`
	if err := os.MkdirAll(filepath.Join(coreDir, "src/main/java/demo/core"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(coreDir, "build.gradle"), []byte(coreBuild), 0o644)
	_ = os.WriteFile(filepath.Join(coreDir, "src/main/java/demo/core/CoreUtil.java"), []byte(coreJava), 0o644)

	appDir := filepath.Join(dir, "app")
	appBuild := `plugins { id 'java' }
version = '1.0'
dependencies {
    implementation project(':core')
}
`
	appJava := `package demo.app;
import demo.core.CoreUtil;
public class Main {
    public static void main(String[] args) {
        System.out.println(CoreUtil.message());
    }
}
`
	if err := os.MkdirAll(filepath.Join(appDir, "src/main/java/demo/app"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(appDir, "build.gradle"), []byte(appBuild), 0o644)
	_ = os.WriteFile(filepath.Join(appDir, "src/main/java/demo/app/Main.java"), []byte(appJava), 0o644)

	// Build app using jv -pl app
	jvOut, err := runToolCmd(t, dir, nil, jvBin, "-pl", "app", "compile")
	if err != nil {
		t.Fatalf("jv -pl app compile failed: %v: %s", err, jvOut)
	}

	coreClass := filepath.Join(coreDir, "build/classes/java/main/demo/core/CoreUtil.class")
	appClass := filepath.Join(appDir, "build/classes/java/main/demo/app/Main.class")
	if _, err := os.Stat(coreClass); err != nil {
		t.Fatalf("expected core class built: %v", err)
	}
	if _, err := os.Stat(appClass); err != nil {
		t.Fatalf("expected app class built: %v", err)
	}

	// Verify the compiled classes run together
	cp := filepath.Join(appDir, "build/classes/java/main") + string(filepath.ListSeparator) + filepath.Join(coreDir, "build/classes/java/main")
	javaOut, err := runToolCmd(t, dir, nil, "java", "-cp", cp, "demo.app.Main")
	if err != nil || !strings.Contains(javaOut, "GRADLE_MULTI_PARITY_SUCCESS") {
		t.Fatalf("running multi-project classes failed: %v: %s", err, javaOut)
	}
}

func TestParityGradle_TaskDelegation(t *testing.T) {
	requireTools(t, "gradle")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	buildGradle := `plugins { id 'java' }
tasks.register('customParityTask') {
    doLast {
        println "DELEGATED_CUSTOM_GRADLE_TASK_RAN"
    }
}
`
	writeGradleProject(t, dir, buildGradle, "rootProject.name = 'taskapp'", "")

	jvOut, err := runToolCmd(t, dir, nil, jvBin, "customParityTask")
	if err != nil {
		t.Fatalf("jv customParityTask failed: %v: %s", err, jvOut)
	}
	if !strings.Contains(jvOut, "DELEGATED_CUSTOM_GRADLE_TASK_RAN") {
		t.Errorf("expected custom task output in: %s", jvOut)
	}
}

func TestParityGradle_ModelCaching(t *testing.T) {
	requireTools(t, "gradle", "javac")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	buildGradle := `plugins { id 'java' }
version = '1.0'
`
	mainJava := `package demo;
public class App {}
`
	writeGradleProject(t, dir, buildGradle, "rootProject.name = 'cacheapp'", mainJava)

	// First run asks Gradle for model
	jvOut1, err := runToolCmd(t, dir, nil, jvBin, "compile")
	if err != nil {
		t.Fatalf("first compile failed: %v: %s", err, jvOut1)
	}
	if !strings.Contains(jvOut1, "asking Gradle for the project model") {
		t.Errorf("expected model query in first run: %s", jvOut1)
	}

	// Second run should use cached model
	jvOut2, err := runToolCmd(t, dir, nil, jvBin, "compile")
	if err != nil {
		t.Fatalf("second compile failed: %v: %s", err, jvOut2)
	}
	if strings.Contains(jvOut2, "asking Gradle for the project model") {
		t.Errorf("expected cached model in second run: %s", jvOut2)
	}

	// Edit build.gradle to invalidate cache
	_ = os.WriteFile(filepath.Join(dir, "build.gradle"), []byte(buildGradle+"\n// modified"), 0o644)
	jvOut3, err := runToolCmd(t, dir, nil, jvBin, "compile")
	if err != nil {
		t.Fatalf("third compile failed: %v: %s", err, jvOut3)
	}
	if !strings.Contains(jvOut3, "asking Gradle for the project model") {
		t.Errorf("expected cache invalidation after build.gradle edit: %s", jvOut3)
	}
}
