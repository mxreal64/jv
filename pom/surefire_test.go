// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package pom

import (
	"reflect"
	"strings"
	"testing"
)

func TestSurefireConfigIsRead(t *testing.T) {
	m, err := ParsePOMBytes([]byte(`<project><build><plugins><plugin><artifactId>maven-surefire-plugin</artifactId><configuration>
<includes><include>**/*IT.java</include></includes>
<excludes><exclude>**/Slow*.java</exclude></excludes>
<groups>fast, unit</groups><excludedGroups>flaky</excludedGroups>
<argLine>-Xmx512m</argLine>
<systemPropertyVariables><my.prop>hello</my.prop><other>x</other></systemPropertyVariables>
</configuration></plugin></plugins></build></project>`))
	if err != nil {
		t.Fatal(err)
	}
	if u := m.Build.Unsupported(); len(u) != 0 {
		t.Errorf("plain surefire configuration must be supported, got %v", u)
	}
	c := m.Build.SurefireConfig()
	if !reflect.DeepEqual(c.Includes, []string{"**/*IT.java"}) || !reflect.DeepEqual(c.Excludes, []string{"**/Slow*.java"}) {
		t.Errorf("includes/excludes = %v / %v", c.Includes, c.Excludes)
	}
	if c.Groups != "fast, unit" || c.ExcludedGroups != "flaky" || c.ArgLine != "-Xmx512m" {
		t.Errorf("groups/excludedGroups/argLine = %q / %q / %q", c.Groups, c.ExcludedGroups, c.ArgLine)
	}
	if c.SysProps["my.prop"] != "hello" || c.SysProps["other"] != "x" || len(c.SysProps) != 2 {
		t.Errorf("system properties = %v", c.SysProps)
	}
}

func TestSurefireFormsThatMustDelegate(t *testing.T) {
	m, err := ParsePOMBytes([]byte(`<project><build><plugins><plugin><artifactId>maven-surefire-plugin</artifactId><configuration>
<argLine>@{argLine} -Xmx1g</argLine><includes><include>%regex[.*IT]</include></includes>
<forkCount>2</forkCount></configuration></plugin></plugins></build></project>`))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(m.Build.Unsupported(), "|")
	for _, want := range []string{"late binding", "%regex", "forkCount"} {
		if !strings.Contains(got, want) {
			t.Errorf("Unsupported() = %q, missing %q", got, want)
		}
	}
}

func TestPluginManagementConfigIsRead(t *testing.T) {
	m, err := ParsePOMBytes([]byte(`<project><build><pluginManagement><plugins><plugin>
<artifactId>maven-compiler-plugin</artifactId><configuration><release>17</release></configuration>
</plugin></plugins></pluginManagement></build></project>`))
	if err != nil {
		t.Fatal(err)
	}
	if v := m.Build.CompilerProps()["maven.compiler.release"]; v != "17" {
		t.Errorf("release from pluginManagement = %q, want 17", v)
	}
}
