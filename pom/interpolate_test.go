// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package pom

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// The original regexp implementation of Interpolate, kept as the oracle for the hand-written one.
var interpolateRefRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func interpolateRef(val string, props map[string]string) string {
	for i := 0; i < 10 && strings.Contains(val, "${"); i++ {
		next := interpolateRefRe.ReplaceAllStringFunc(val, func(m string) string {
			if v, ok := props[m[2:len(m)-1]]; ok {
				return v
			}
			return m
		})
		if next == val {
			break
		}
		val = next
	}
	return val
}

func TestInterpolateMatchesRegexpReference(t *testing.T) {
	props := map[string]string{
		"a": "1", "b": "${a}.2", "c": "${b}-${a}", "self": "${self}", "x": "${y}", "y": "${x}",
		"empty": "", "a${b": "weird", "env.HOME": "/home/u", "project.version": "1.0-SNAPSHOT",
	}
	fixed := []string{"", "plain", "${a}", "${b}", "${c}", "${a}${a}", "${}", "${}${a}", "${}}", "${", "${a", "a}",
		"$${a}", "${${a}}", "${a${b}", "${a ${b}", "${self}", "${x}", "${missing}", "pre-${a}-post",
		"${env.HOME}/m2", "${project.version}", "${empty}x", "{${a}}", "$ {a}", "${a}\n${b}"}
	check := func(in string) {
		t.Helper()
		if got, want := Interpolate(in, props), interpolateRef(in, props); got != want {
			t.Errorf("Interpolate(%q) = %q; the reference regexp implementation gives %q", in, got, want)
		}
	}
	for _, in := range fixed {
		check(in)
	}
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"$", "{", "}", "a", "b", "c", "x", "y", ".", "self", "${a}", "${b}", "${"}
	for n := 0; n < 5000; n++ {
		var sb strings.Builder
		for k := rng.Intn(8); k >= 0; k-- {
			sb.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		check(sb.String())
	}
}
