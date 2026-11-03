// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package compiler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpstreamStatePrefersABI(t *testing.T) {
	dir := t.TempDir()
	if _, ok := upstreamState(dir); ok {
		t.Fatal("an unbuilt directory has no state")
	}
	if err := os.WriteFile(filepath.Join(dir, ".jv-stamp"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, ok := upstreamState(dir); !ok || !strings.HasPrefix(s, "stamp:") {
		t.Fatalf("want stamp state, got %q %v", s, ok)
	}
	if err := os.WriteFile(filepath.Join(dir, abiFile), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, ok := upstreamState(dir); !ok || s != "abi:abc" {
		t.Fatalf("want abi state, got %q %v", s, ok)
	}
}

// A downstream module is rebuilt only when the upstream ABI changed, not on every upstream rebuild.
func TestDownstreamSkippedWhenUpstreamABIUnchanged(t *testing.T) {
	if _, err := exec.LookPath(javacBinary()); err != nil {
		t.Skip("javac not available")
	}
	ctx := context.Background()
	root := t.TempDir()
	upSrc, upOut := filepath.Join(root, "up", "src"), filepath.Join(root, "up", "classes")
	downSrc, downOut := filepath.Join(root, "down", "src"), filepath.Join(root, "down", "classes")
	for _, d := range []string{upSrc, downSrc} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	up := func(body, extra string) string {
		return "public class Up { public static int n() { return " + body + "; } " + extra + " }"
	}
	write(filepath.Join(downSrc, "Down.java"), "public class Down { int x = Up.n(); }")
	opt := Options{Encoding: "UTF-8"}

	compile := func(src, out string, cp []string) bool {
		t.Helper()
		ran, err := Compile(ctx, src, out, cp, opt)
		if err != nil {
			t.Fatal(err)
		}
		return ran
	}

	write(filepath.Join(upSrc, "Up.java"), up("1", ""))
	if !compile(upSrc, upOut, nil) || !compile(downSrc, downOut, []string{upOut}) {
		t.Fatal("first builds must run javac")
	}
	if compile(downSrc, downOut, []string{upOut}) {
		t.Fatal("unchanged downstream must be skipped")
	}

	write(filepath.Join(upSrc, "Up.java"), up("2", "private int hidden; private void h() { Runnable r = () -> {}; }")) // body + private members only
	if !compile(upSrc, upOut, nil) {
		t.Fatal("upstream source changed, it must rebuild")
	}
	if compile(downSrc, downOut, []string{upOut}) {
		t.Error("downstream was rebuilt although the upstream ABI did not change")
	}

	write(filepath.Join(upSrc, "Up.java"), up("2", "public static void added() {}")) // new public API
	if !compile(upSrc, upOut, nil) {
		t.Fatal("upstream must rebuild")
	}
	if !compile(downSrc, downOut, []string{upOut}) {
		t.Error("downstream must be rebuilt after an upstream ABI change")
	}
	if compile(downSrc, downOut, []string{upOut}) {
		t.Error("downstream must settle after one rebuild")
	}
}
