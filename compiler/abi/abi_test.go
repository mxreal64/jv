// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package abi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func javac(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac not available")
	}
	return p
}

// sig compiles the given sources (file name -> contents) with javac and fingerprints the result.
func sig(t *testing.T, files map[string]string) string {
	t.Helper()
	jc := javac(t)
	dir := t.TempDir()
	src, out := filepath.Join(dir, "src"), filepath.Join(dir, "out")
	var paths []string
	for name, body := range files {
		p := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	args := append([]string{"--release", "17", "-d", out}, paths...)
	if b, err := exec.Command(jc, args...).CombinedOutput(); err != nil {
		t.Fatalf("javac failed: %v\n%s", err, b)
	}
	s, err := Signature(out)
	if err != nil || s == "" {
		t.Fatalf("Signature: %q %v", s, err)
	}
	return s
}

const base = `package demo;
import java.util.function.Supplier;
public class Lib {
    public static final int MAX = 10;
    public static final long BIG = 1L << 40;
    public static final double HALF = 0.5;
    public static final String NAME = "lib";
    private int hidden;
    public int run() { return 1; }
    public <T extends Comparable<T>> T pick(T a, T b) { return a; }
}
`

func lib(mod func(string) string) map[string]string {
	return map[string]string{"demo/Lib.java": mod(base)}
}

func TestIgnoresWhatDownstreamCannotSee(t *testing.T) {
	want := sig(t, lib(func(s string) string { return s }))
	cases := map[string]func(string) string{
		"method body": func(s string) string {
			return strings.Replace(s, "public int run() { return 1; }", "public int run() { int x = 41; return x + 1; }", 1)
		},
		"private field and method": func(s string) string {
			return strings.Replace(s, "private int hidden;", "private int hidden; private long other; private void helper() {}", 1)
		},
		"lambda in a body": func(s string) string {
			return strings.Replace(s, "public int run() { return 1; }", "public int run() { Supplier<Integer> f = () -> 2; return f.get(); }", 1)
		},
		"anonymous class in a body": func(s string) string {
			return strings.Replace(s, "public int run() { return 1; }", "public int run() { return new Object() { int v() { return 3; } }.v(); }", 1)
		},
		"private nested class": func(s string) string {
			return strings.Replace(s, "private int hidden;", "private int hidden; private static class Helper { int a; }", 1)
		},
		"static initializer": func(s string) string {
			return strings.Replace(s, "private int hidden;", "private int hidden; static { System.getProperty(\"x\"); }", 1)
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := sig(t, lib(mod)); got != want {
				t.Errorf("%s must not change the ABI", name)
			}
		})
	}
}

func TestDetectsEveryVisibleChange(t *testing.T) {
	want := sig(t, lib(func(s string) string { return s }))
	cases := map[string]func(string) string{
		"new public method": func(s string) string {
			return strings.Replace(s, "private int hidden;", "private int hidden; public void extra() {}", 1)
		},
		"new protected method": func(s string) string {
			return strings.Replace(s, "private int hidden;", "private int hidden; protected void extra() {}", 1)
		},
		"new package-private method": func(s string) string {
			return strings.Replace(s, "private int hidden;", "private int hidden; void extra() {}", 1)
		},
		"int constant": func(s string) string { return strings.Replace(s, "MAX = 10", "MAX = 11", 1) },
		"long constant": func(s string) string {
			return strings.Replace(s, "BIG = 1L << 40", "BIG = 1L << 41", 1)
		},
		"double constant": func(s string) string { return strings.Replace(s, "HALF = 0.5", "HALF = 0.75", 1) },
		"string constant": func(s string) string { return strings.Replace(s, `NAME = "lib"`, `NAME = "lib2"`, 1) },
		"return type":     func(s string) string { return strings.Replace(s, "public int run()", "public long run()", 1) },
		"parameter list":  func(s string) string { return strings.Replace(s, "public int run()", "public int run(int n)", 1) },
		"generic bound": func(s string) string {
			return strings.Replace(s, "T extends Comparable<T>", "T extends Comparable<? super T>", 1)
		},
		"declared exception": func(s string) string {
			return strings.Replace(s, "public int run() {", "public int run() throws java.io.IOException {", 1)
		},
		"deprecation": func(s string) string {
			return strings.Replace(s, "public int run()", "@Deprecated public int run()", 1)
		},
		"made final": func(s string) string { return strings.Replace(s, "public class Lib", "public final class Lib", 1) },
		"superclass": func(s string) string {
			return strings.Replace(s, "public class Lib", "public class Lib extends java.util.ArrayList<String>", 1)
		},
		"interface": func(s string) string {
			return strings.Replace(s, "public class Lib", "public class Lib implements Runnable", 1)
		},
		"removed public field": func(s string) string {
			return strings.Replace(s, "public static final double HALF = 0.5;", "", 1)
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := mod(base)
			if name == "interface" { // Runnable needs a void run()
				src = strings.Replace(src, "public int run()", "public void run() {} public int run2()", 1)
			}
			if got := sig(t, map[string]string{"demo/Lib.java": src}); got == want {
				t.Errorf("%s must change the ABI", name)
			}
		})
	}
}

func TestAnnotationsAreInterface(t *testing.T) {
	ann := func(ret string) map[string]string {
		return map[string]string{
			"demo/Ann.java": "package demo; import java.lang.annotation.*; @Retention(RetentionPolicy." + ret + ") public @interface Ann { int value() default 1; }",
			"demo/Use.java": "package demo; @Ann(2) public class Use { @Ann public void m() {} }",
		}
	}
	runtime := sig(t, ann("RUNTIME"))
	if sig(t, ann("RUNTIME")) != runtime {
		t.Error("fingerprints must be deterministic")
	}
	if sig(t, ann("CLASS")) == runtime {
		t.Error("changing an annotation's retention changes what dependants compile to")
	}
	changed := ann("RUNTIME")
	changed["demo/Ann.java"] = strings.Replace(changed["demo/Ann.java"], "default 1", "default 2", 1)
	if sig(t, changed) == runtime {
		t.Error("changing an annotation default must change the ABI")
	}
	changed = ann("RUNTIME")
	changed["demo/Use.java"] = strings.Replace(changed["demo/Use.java"], "@Ann(2)", "@Ann(3)", 1)
	if sig(t, changed) == runtime {
		t.Error("changing an annotation value on a class must change the ABI")
	}
}

func TestRecordsEnumsAndSealed(t *testing.T) {
	src := func(extra string) map[string]string {
		return map[string]string{
			"demo/R.java": "package demo; public record R(int a, String b) { " + extra + " }",
			"demo/E.java": "package demo; public enum E { ONE, TWO }",
		}
	}
	base := sig(t, src(""))
	if sig(t, src("public int twice() { return a * 2; }")) == base {
		t.Error("new public record method must change the ABI")
	}
	if sig(t, src("private int hidden() { return a; }")) != base {
		t.Error("private record method must not change the ABI")
	}
	changed := src("")
	changed["demo/E.java"] = "package demo; public enum E { ONE, TWO, THREE }"
	if sig(t, changed) == base {
		t.Error("a new enum constant must change the ABI")
	}
}

func TestRejectsGarbageAndFallsBackToRawBytes(t *testing.T) {
	if _, err := Fingerprint([]byte("not a class")); err == nil {
		t.Error("expected an error for non-class data")
	}
	dir := t.TempDir()
	odd := filepath.Join(dir, "Odd.class")
	if err := os.WriteFile(odd, []byte("garbage-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	s1, err := Signature(dir)
	if err != nil || s1 == "" {
		t.Fatal(s1, err)
	}
	if err := os.WriteFile(odd, []byte("garbage-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s2, _ := Signature(dir); s2 == s1 {
		t.Error("an unparseable class file must still be fingerprinted by its bytes")
	}
	if s, err := Signature(filepath.Join(dir, "missing")); s != "" || err != nil {
		t.Errorf("missing directory: got %q, %v", s, err)
	}
}

func TestTruncatedClassIsRejected(t *testing.T) {
	jc := javac(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "A.java")
	_ = os.WriteFile(src, []byte("public class A { public int f; }"), 0o644)
	if b, err := exec.Command(jc, "-d", dir, src).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	data, err := os.ReadFile(filepath.Join(dir, "A.class"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Fingerprint(data); err != nil {
		t.Fatalf("valid class rejected: %v", err)
	}
	for _, cut := range []int{10, len(data) / 2, len(data) - 3} {
		if _, err := Fingerprint(data[:cut]); err == nil {
			t.Errorf("class truncated at %d bytes must be rejected", cut)
		}
	}
}
