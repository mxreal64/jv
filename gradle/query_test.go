// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package gradle

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeGradle writes a script that stands in for gradle: it records its working directory and
// arguments next to itself, then runs body (a sh snippet that may use the -Djv.model.out path
// in $out).
func fakeGradle(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gradle is a POSIX shell script")
	}
	script := "#!/bin/sh\n" +
		"pwd -P > \"$0.cwd\"\n" + // physical path: macOS temp dirs are symlinks (/var -> /private/var)
		"printf '%s\\n' \"$@\" > \"$0.args\"\n" +
		"out=\n" +
		"for a in \"$@\"; do case \"$a\" in -Djv.model.out=*) out=\"${a#-Djv.model.out=}\" ;; esac; done\n" +
		body + "\n"
	path := filepath.Join(dir, "gradle")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func skipIfNotExecutable(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "permission denied") {
		t.Skip("cannot execute scripts from the temp dir")
	}
}

func TestQueryCommandLineAndModel(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	modelFile := filepath.Join(dir, "model.json")
	if err := os.WriteFile(modelFile, []byte(`{"root":"/w","projects":[{"path":":","dir":"/w","name":"w","java":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	g := fakeGradle(t, dir, `cp "`+modelFile+`" "$out"`)

	m, err := Query(context.Background(), g, root, []string{"-Pfoo=1"}, true)
	skipIfNotExecutable(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if m.Root != "/w" || len(m.Projects) != 1 || !m.Projects[0].Java {
		t.Fatalf("model not decoded: %+v", m)
	}

	data, err := os.ReadFile(g + ".args")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, want := range []string{"-q", "--console=plain", "--no-configuration-cache", "--init-script", "--offline", "-Pfoo=1"} {
		found := false
		for _, a := range args {
			found = found || a == want
		}
		if !found {
			t.Errorf("gradle was not given %q; args: %v", want, args)
		}
	}
	if last := args[len(args)-1]; last != "jvModel" {
		t.Errorf("the last argument must be the jvModel task, got %q (args: %v)", last, args)
	}
	for i, a := range args {
		if a == "--init-script" && (i+1 >= len(args) || !strings.HasSuffix(args[i+1], "jv-model.init.gradle")) {
			t.Errorf("--init-script must be followed by the init script, args: %v", args)
		}
	}
	cwd, _ := os.ReadFile(g + ".cwd")
	wantDir, _ := filepath.EvalSymlinks(root)
	if got := strings.TrimSpace(string(cwd)); got != wantDir {
		t.Errorf("gradle ran in %q; want the build root %q", got, wantDir)
	}
}

func TestQueryWithoutOfflineDoesNotPassIt(t *testing.T) {
	dir := t.TempDir()
	g := fakeGradle(t, dir, `echo '{"root":"/w","projects":[]}' > "$out"`)
	_, err := Query(context.Background(), g, dir, nil, false)
	skipIfNotExecutable(t, err)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(g + ".args")
	if strings.Contains(string(data), "--offline") {
		t.Errorf("--offline passed although offline=false:\n%s", data)
	}
}

func TestQueryFailureModes(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"gradle fails", `echo 'boom: could not resolve X' >&2; exit 3`, "boom: could not resolve X"},
		{"no model written", `exit 0`, "no model"},
		{"model is not json", `echo 'not json' > "$out"`, "unreadable model"},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		g := fakeGradle(t, dir, tt.body)
		_, err := Query(context.Background(), g, dir, nil, false)
		skipIfNotExecutable(t, err)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v; want it to contain %q", tt.name, err, tt.want)
		}
	}
}
