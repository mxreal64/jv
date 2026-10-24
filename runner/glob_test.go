// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package runner

import (
	"regexp"
	"testing"
)

func fullMatch(t *testing.T, re, s string) bool {
	t.Helper()
	ok, err := regexp.MatchString("^(?:"+re+")$", s)
	if err != nil {
		t.Fatalf("invalid regex %q: %v", re, err)
	}
	return ok
}

func TestAntGlobToClassRegex(t *testing.T) {
	tests := []struct {
		glob string
		yes  []string
		no   []string
	}{
		{"**/*Test.java", []string{"FooTest", "a.b.FooTest"}, []string{"a.b.Foo", "FooTest2", "a.FooTestX"}},
		{"**/Test*.java", []string{"TestFoo", "a.TestFoo"}, []string{"a.FooTest"}},
		{"**/*TestCase.java", []string{"a.FooTestCase"}, []string{"a.FooTest"}},
		{"*IT", []string{"FooIT"}, []string{"a.FooIT"}}, // no **/ : default package only, like Surefire
		{"com/acme/**/*Spec.java", []string{"com.acme.FooSpec", "com.acme.x.y.FooSpec"}, []string{"org.FooSpec", "com.acmeX.FooSpec"}},
		{"**/Foo$*.java", []string{"a.Foo$Bar"}, []string{"a.FooBar"}},
		{"**/Fo?Test.java", []string{"a.FooTest"}, []string{"a.FoTest", "a.FoooTest"}},
	}
	for _, tt := range tests {
		re := AntGlobToClassRegex(tt.glob)
		for _, n := range tt.yes {
			if !fullMatch(t, re, n) {
				t.Errorf("%q (%s) should match %q", tt.glob, re, n)
			}
		}
		for _, n := range tt.no {
			if fullMatch(t, re, n) {
				t.Errorf("%q (%s) should not match %q", tt.glob, re, n)
			}
		}
	}
}
