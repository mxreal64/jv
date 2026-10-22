// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package pom

import (
	"testing"
)

func TestParsePOMBasic(t *testing.T) {
	xml := `<project xmlns="http://maven.apache.org/POM/4.0.0">
	<groupId>com.example</groupId>
	<artifactId>my-app</artifactId>
	<version>1.0.0</version>
	<properties>
	<junit.version>5.10.0</junit.version>
	</properties>
	<dependencies>
	<dependency>
	<groupId>org.junit.jupiter</groupId>
	<artifactId>junit-jupiter</artifactId>
	<version>${junit.version}</version>
	<scope>test</scope>
	</dependency>
	</dependencies>
	</project>`

	m, err := ParsePOMBytes([]byte(xml))
	if err != nil {
		t.Fatalf("ParsePOMBytes failed: %v", err)
	}
	if m.GroupID != "com.example" || m.ArtifactID != "my-app" || m.Version != "1.0.0" {
		t.Fatalf("unexpected GAV: %s:%s:%s", m.GroupID, m.ArtifactID, m.Version)
	}
	if len(m.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(m.Dependencies))
	}
	dep := m.Dependencies[0]
	if dep.GroupID != "org.junit.jupiter" || dep.ArtifactID != "junit-jupiter" {
		t.Errorf("unexpected dep G:A: %s:%s", dep.GroupID, dep.ArtifactID)
	}

	props := m.PropMap()
	interpVer := Interpolate(dep.Version, props)
	if interpVer != "5.10.0" {
		t.Errorf("Interpolate version = %q; want 5.10.0", interpVer)
	}
}

func TestParsePOMInheritance(t *testing.T) {
	xml := `<project xmlns="http://maven.apache.org/POM/4.0.0">
	<parent>
	<groupId>com.example.parent</groupId>
	<artifactId>parent-pom</artifactId>
	<version>2.0.0</version>
	</parent>
	<artifactId>child-module</artifactId>
	</project>`

	m, err := ParsePOMBytes([]byte(xml))
	if err != nil {
		t.Fatalf("ParsePOMBytes failed: %v", err)
	}
	if m.GroupID != "com.example.parent" {
		t.Errorf("expected inherited GroupID com.example.parent, got %q", m.GroupID)
	}
	if m.Version != "2.0.0" {
		t.Errorf("expected inherited Version 2.0.0, got %q", m.Version)
	}
	if m.Packaging != "jar" {
		t.Errorf("expected default packaging jar, got %q", m.Packaging)
	}
}

func TestJDKMatches(t *testing.T) {
	for _, c := range []struct {
		spec, ver string
		want      bool
	}{
		{"17", "17.0.2", true}, {"1.8", "1.8.0_292", true}, {"17", "170.1", false},
		{"!1.8", "17.0.2", true}, {"!17", "17.0.2", false},
		{"[9,)", "17.0.2", true}, {"[9,)", "1.8.0_292", false}, {"[1.8,)", "17.0.2", true},
		{"(,1.8]", "1.8.0_292", true}, {"[9,12)", "17", false}, {"[9,12)", "11.0.1", true},
	} {
		if got := JDKMatches(c.spec, c.ver); got != c.want {
			t.Errorf("JDKMatches(%q,%q)=%v want %v", c.spec, c.ver, got, c.want)
		}
	}
}

func TestProfileEnvActivation(t *testing.T) {
	const x = `<project><profiles>
 <profile><id>j9</id><activation><jdk>[9,)</jdk></activation><properties><j>9</j></properties></profile>
 <profile><id>win</id><activation><os><family>windows</family></os></activation><properties><w>1</w></properties></profile>
 <profile><id>both</id><activation><jdk>17</jdk><os><family>unix</family></os></activation><properties><b>1</b></properties></profile>
</profiles></project>`
	props := func(e Env) map[string]string {
		m, err := ParsePOMBytes([]byte(x))
		if err != nil {
			t.Fatal(err)
		}
		if u := m.ApplyProfilesEnv(e); len(u) != 0 {
			t.Fatalf("unexpected unsupported profiles: %v", u)
		}
		return m.PropMap()
	}
	got := props(Env{JDK: "17.0.2", OS: "linux", Arch: "amd64"})
	if got["j"] != "9" || got["w"] != "" || got["b"] != "1" {
		t.Fatalf("linux/17: %v", got)
	}
	got = props(Env{JDK: "1.8.0_292", OS: "windows", Arch: "amd64"})
	if got["j"] != "" || got["w"] != "1" || got["b"] != "" {
		t.Fatalf("windows/8: %v", got)
	}
	m, _ := ParsePOMBytes([]byte(x))
	if u := m.ApplyProfiles(nil, nil); len(u) != 3 {
		t.Fatalf("without an environment all three profiles are unevaluable, got %v", u)
	}
}
