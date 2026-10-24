// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package pom

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildUnsupportedAndCompilerProps(t *testing.T) {
	m, err := ParsePOMBytes([]byte(`<project><artifactId>a</artifactId><build>
<sourceDirectory>src</sourceDirectory>
<plugins>
<plugin><artifactId>maven-compiler-plugin</artifactId><configuration><release>17</release><showWarnings>true</showWarnings></configuration></plugin>
<plugin><artifactId>maven-shade-plugin</artifactId><executions><execution><phase>package</phase><goals><goal>shade</goal></goals></execution></executions></plugin>
</plugins></build></project>`))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(m.Build.Unsupported(), "|")
	if !strings.Contains(got, "build/sourceDirectory") || !strings.Contains(got, "maven-shade-plugin: executions") {
		t.Errorf("Unsupported() = %q", got)
	}
	if strings.Contains(got, "maven-compiler-plugin") {
		t.Errorf("plain compiler release/showWarnings must be supported, got %q", got)
	}
	if v := m.Build.CompilerProps()["maven.compiler.release"]; v != "17" {
		t.Errorf("release = %q, want 17", v)
	}

	m, err = ParsePOMBytes([]byte(`<project><build><plugins><plugin><artifactId>maven-compiler-plugin</artifactId>
<configuration><compilerArgs><arg>-Xlint</arg></compilerArgs></configuration></plugin></plugins></build></project>`))
	if err != nil {
		t.Fatal(err)
	}
	if u := m.Build.Unsupported(); len(u) != 1 || !strings.Contains(u[0], "compilerArgs") {
		t.Errorf("compilerArgs should be unsupported, got %v", u)
	}
}

func TestProfileModules(t *testing.T) {
	const doc = `<project><modules><module>a</module></modules><profiles><profile><id>extra</id><modules><module>b</module></modules></profile></profiles></project>`
	m, err := ParsePOMBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	m.ApplyProfiles(map[string]bool{"extra": true}, nil)
	if !reflect.DeepEqual(m.Modules, []string{"a", "b"}) {
		t.Errorf("modules with -P extra = %v", m.Modules)
	}
	m, _ = ParsePOMBytes([]byte(doc))
	m.ApplyProfiles(nil, nil)
	if !reflect.DeepEqual(m.Modules, []string{"a"}) {
		t.Errorf("modules without profile = %v", m.Modules)
	}
}
