// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package runner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestClassRegex(t *testing.T) {
	tests := []struct {
		filter, class string
		want          bool
	}{
		{"AppTest", "com.x.AppTest", true},
		{"AppTest", "AppTest", true},
		{"AppTest", "com.x.MyAppTest", false},
		{"*Test", "com.x.FooTest", true},
		{"*IT", "com.x.FooIT", true},
		{"Foo*", "com.x.FooBar", true},
		{"Foo*", "com.x.BarFoo", false},
		{"com.x.*Test", "com.x.FooTest", true},
		{"com.x.*Test", "com.y.FooTest", false},
		{"com.x.Foo", "com.x.Foo", true},
		{"com.x.Foo", "com.xAFoo", false},
	}
	for _, tt := range tests {
		got, err := regexp.MatchString("^(?:"+classRegex(tt.filter)+")$", tt.class)
		if err != nil {
			t.Fatalf("classRegex(%q) is not a valid regex: %v", tt.filter, err)
		}
		if got != tt.want {
			t.Errorf("classRegex(%q) matching %q = %v; want %v", tt.filter, tt.class, got, tt.want)
		}
	}
}

func TestJavaBinary(t *testing.T) {
	jb := javaBinary()
	if jb == "" {
		t.Fatal("expected non-empty java binary")
	}

	t.Setenv("JAVA_HOME", "")
	if javaBinary() != "java" {
		t.Fatalf("expected fallback to 'java'")
	}
}

func TestRunTestsIn_NoScanDir(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		Scan: "/tmp/non-existent-scan-dir-jv-test",
	}
	if err := RunTestsIn(ctx, cfg, "", nil); err != nil {
		t.Fatalf("expected nil when scan dir missing, got: %v", err)
	}

	// Default scan also nil when target/test-classes doesn't exist in working directory
	if err := RunTests(ctx, "", nil); err != nil {
		t.Fatalf("expected nil for default missing target/test-classes, got: %v", err)
	}
}

func TestRunTestsIn_ArgumentAssembly(t *testing.T) {
	dir := t.TempDir()
	scanDir := filepath.Join(dir, "test-classes")
	_ = os.MkdirAll(scanDir, 0o755)
	reportsDir := filepath.Join(dir, "reports")

	cfg := Config{
		Scan:           scanDir,
		Reports:        reportsDir,
		JVMArgs:        []string{"-Xmx256m"},
		SysProps:       map[string]string{"env": "test", "foo": "bar"},
		Groups:         "fast, unit",
		ExcludedGroups: "slow",
		Includes:       []string{".*Test"},
		Excludes:       []string{".*SlowTest"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = RunTestsIn(ctx, cfg, "launcher.jar", []string{"lib.jar"}, "MyClass#testMethod")
	_ = RunTestsIn(ctx, cfg, "", []string{"lib.jar"}, "MyClass*")
	_ = RunTestsIn(ctx, cfg, "", []string{"lib.jar"}, "com.example.MyTest")
	_ = RunTestsIn(ctx, cfg, "", []string{"lib.jar"})
}
