// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package main

import (
	"regexp"
	"testing"

	"github.com/jv-build/jv/pom"
)

func matchesAny(t *testing.T, res []string, name string) bool {
	t.Helper()
	for _, re := range res {
		ok, err := regexp.MatchString("^(?:"+re+")$", name)
		if err != nil {
			t.Fatalf("invalid regex %q: %v", re, err)
		}
		if ok {
			return true
		}
	}
	return false
}

// The JUnit launcher matches class-name patterns against the whole fully-qualified name, so the
// Surefire defaults must work for classes in packages.
func TestSurefireDefaultPatternsMatchQualifiedNames(t *testing.T) {
	inc, exc := surefirePatterns(pom.SurefireCfg{})
	if len(exc) != 0 {
		t.Errorf("no default excludes expected, got %v", exc)
	}
	for _, n := range []string{"com.acme.FooTest", "TestFoo", "a.b.FooTests", "a.FooTestCase", "FooTest"} {
		if !matchesAny(t, inc, n) {
			t.Errorf("default includes should match %q", n)
		}
	}
	for _, n := range []string{"com.acme.Foo", "a.FooTestHelper", "a.FooIT"} {
		if matchesAny(t, inc, n) {
			t.Errorf("default includes should not match %q", n)
		}
	}
}

func TestSurefireExplicitIncludesReplaceDefaults(t *testing.T) {
	inc, _ := surefirePatterns(pom.SurefireCfg{Includes: []string{"**/*IT.java"}, Excludes: []string{"**/Slow*.java"}})
	if !matchesAny(t, inc, "a.FooIT") || matchesAny(t, inc, "a.FooTest") {
		t.Errorf("explicit includes must replace the defaults, got %v", inc)
	}
	_, exc := surefirePatterns(pom.SurefireCfg{Excludes: []string{"**/Slow*.java"}})
	if !matchesAny(t, exc, "a.SlowIT") {
		t.Errorf("excludes not converted: %v", exc)
	}
}
