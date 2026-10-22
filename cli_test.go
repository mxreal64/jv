// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package main

import (
	"reflect"
	"testing"

	"github.com/jv-build/jv/bridge"
)

func TestParseArgs(t *testing.T) {
	args := []string{
		"-o",
		"-X",
		"-s", "my-settings.xml",
		"-P", "profile1,profile2",
		"-D", "skipTests",
		"-Dfoo=bar",
		"-pl", "moduleA,moduleB",
		"compile",
		"test",
	}

	o := parseArgs(args)
	if !o.offline {
		t.Errorf("expected offline=true")
	}
	if !o.verbose {
		t.Errorf("expected verbose=true")
	}
	if o.settingsFile != "my-settings.xml" {
		t.Errorf("settingsFile = %q; want my-settings.xml", o.settingsFile)
	}
	if !o.profiles["profile1"] || !o.profiles["profile2"] {
		t.Errorf("expected profiles [profile1, profile2], got %v", o.profiles)
	}
	if o.props["skipTests"] != "true" || o.props["foo"] != "bar" {
		t.Errorf("expected props {skipTests: true, foo: bar}, got %v", o.props)
	}
	if !o.skipTests {
		t.Errorf("expected skipTests=true")
	}
	if !reflect.DeepEqual(o.pl, []string{"moduleA", "moduleB"}) {
		t.Errorf("expected pl [moduleA, moduleB], got %v", o.pl)
	}
	if !reflect.DeepEqual(o.goals, []string{"compile", "test"}) {
		t.Errorf("expected goals [compile, test], got %v", o.goals)
	}
}

func TestTranslateGradleArgs(t *testing.T) {
	tests := []struct {
		mavenArgs  []string
		gradleArgs []string
	}{
		{
			mavenArgs:  []string{"clean", "compile", "-o"},
			gradleArgs: []string{"--offline", "clean", "classes"},
		},
		{
			mavenArgs:  []string{"test", "-DskipTests"},
			gradleArgs: []string{"-x", "test", "test"},
		},
		{
			mavenArgs:  []string{"package", "-q"},
			gradleArgs: []string{"--quiet", "build"},
		},
		{
			mavenArgs:  []string{"install"},
			gradleArgs: []string{"publishToMavenLocal"},
		},
		{
			mavenArgs:  []string{"tree"},
			gradleArgs: []string{"dependencies"},
		},
		{
			mavenArgs:  []string{"compile", "-pl", "core"},
			gradleArgs: []string{":core:classes"},
		},
	}

	for _, tt := range tests {
		got := bridge.TranslateMavenArgs(tt.mavenArgs)
		if !reflect.DeepEqual(got, tt.gradleArgs) {
			t.Errorf("TranslateMavenArgs(%v) = %v; want %v", tt.mavenArgs, got, tt.gradleArgs)
		}
	}
}

func TestParseArgsMavenCompatFlags(t *testing.T) {
	o := parseArgs([]string{"-B", "-e", "-ntp", "-V", "-am", "-pl", "a", "compile"})
	if len(o.unknown) != 0 {
		t.Errorf("compat flags should be accepted, got unknown %v", o.unknown)
	}
	if !reflect.DeepEqual(o.goals, []string{"compile"}) || !reflect.DeepEqual(o.pl, []string{"a"}) {
		t.Errorf("goals=%v pl=%v", o.goals, o.pl)
	}
	o = parseArgs([]string{"-U", "compile"})
	if len(o.unknown) != 0 || !o.updateSnapshots {
		t.Errorf("-U must be accepted and force snapshot updates, got unknown=%v update=%v", o.unknown, o.updateSnapshots)
	}
	o = parseArgs([]string{"--fail-never", "compile"})
	if !reflect.DeepEqual(o.unknown, []string{"--fail-never"}) {
		t.Errorf("flags jv does not know must be reported as unknown (delegated to mvn), got %v", o.unknown)
	}
}

func TestParseArgsUpdateSnapshots(t *testing.T) {
	if o := parseArgs([]string{"-U", "compile"}); !o.updateSnapshots || len(o.unknown) != 0 {
		t.Fatalf("%+v", o)
	}
	if o := parseArgs([]string{"--update-snapshots", "compile"}); !o.updateSnapshots {
		t.Fatalf("%+v", o)
	}
}
