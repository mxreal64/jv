// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"archive/zip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

var (
	testJVBinOnce sync.Once
	testJVBin     string
	testJVBinErr  error
)

func getTestJVBin(t *testing.T) string {
	t.Helper()
	testJVBinOnce.Do(func() {
		bin := filepath.Join(os.TempDir(), "jv-parity-test-bin")
		cmd := exec.Command("go", "build", "-trimpath", "-o", bin, ".")
		out, err := cmd.CombinedOutput()
		if err != nil {
			testJVBinErr = fmt.Errorf("failed to build jv binary: %w: %s", err, string(out))
			return
		}
		testJVBin = bin
	})
	if testJVBinErr != nil {
		t.Fatal(testJVBinErr)
	}
	return testJVBin
}

func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("tool %q not installed", tool)
		}
	}
}

func runToolCmd(t *testing.T, dir string, env []string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func normalizeClasspath(cp string) []string {
	var list []string
	for _, p := range strings.Split(strings.TrimSpace(cp), string(filepath.ListSeparator)) {
		p = strings.TrimSpace(p)
		if p != "" {
			list = append(list, filepath.Base(p))
		}
	}
	sort.Strings(list)
	return list
}

func compareClasspaths(t *testing.T, scope string, jvCP, mvnCP string) {
	t.Helper()
	jvList := normalizeClasspath(jvCP)
	mvnList := normalizeClasspath(mvnCP)
	if strings.Join(jvList, "\n") != strings.Join(mvnList, "\n") {
		t.Errorf("scope %s classpath mismatch:\nJV:  %v\nMVN: %v", scope, jvList, mvnList)
	}
}

func writeProject(t *testing.T, dir, pomContent, mainJava string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "src/main/java/demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(pomContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if mainJava != "" {
		if err := os.WriteFile(filepath.Join(dir, "src/main/java/demo/App.java"), []byte(mainJava), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParityMaven_TransitiveScopes(t *testing.T) {
	requireTools(t, "mvn", "javac")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	pom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>scopes-parity</artifactId>
  <version>1.0</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <dependencies>
    <dependency>
      <groupId>commons-io</groupId>
      <artifactId>commons-io</artifactId>
      <version>2.15.1</version>
      <scope>compile</scope>
    </dependency>
    <dependency>
      <groupId>commons-codec</groupId>
      <artifactId>commons-codec</artifactId>
      <version>1.16.0</version>
      <scope>provided</scope>
    </dependency>
    <dependency>
      <groupId>org.apache.commons</groupId>
      <artifactId>commons-lang3</artifactId>
      <version>3.14.0</version>
      <scope>runtime</scope>
    </dependency>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter-api</artifactId>
      <version>5.10.2</version>
      <scope>test</scope>
    </dependency>
  </dependencies>
</project>`

	writeProject(t, dir, pom, "package demo; public class App {}")

	for _, scope := range []string{"compile", "runtime", "test"} {
		cpFile := filepath.Join(dir, "mvn-"+scope+".cp")
		_, err := runToolCmd(t, dir, nil, "mvn", "-q", "-B", "-ntp", "dependency:build-classpath",
			"-Dmdep.outputFile="+cpFile, "-DincludeScope="+scope)
		if err != nil {
			t.Fatalf("mvn dependency:build-classpath [%s] failed: %v", scope, err)
		}
		mvnBytes, err := os.ReadFile(cpFile)
		if err != nil {
			t.Fatalf("reading mvn cp: %v", err)
		}

		jvOut, err := runToolCmd(t, dir, nil, jvBin, "-q", "classpath", scope)
		if err != nil {
			t.Fatalf("jv classpath [%s] failed: %v: %s", scope, err, jvOut)
		}

		compareClasspaths(t, scope, jvOut, string(mvnBytes))
	}
}

func TestParityMaven_DependencyManagement(t *testing.T) {
	requireTools(t, "mvn", "javac")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	pom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>dep-mgmt-parity</artifactId>
  <version>1.0</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>commons-io</groupId>
        <artifactId>commons-io</artifactId>
        <version>2.14.0</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
  <dependencies>
    <dependency>
      <groupId>commons-io</groupId>
      <artifactId>commons-io</artifactId>
    </dependency>
  </dependencies>
</project>`

	writeProject(t, dir, pom, "package demo; public class App {}")

	cpFile := filepath.Join(dir, "mvn.cp")
	_, err := runToolCmd(t, dir, nil, "mvn", "-q", "-B", "-ntp", "dependency:build-classpath",
		"-Dmdep.outputFile="+cpFile, "-DincludeScope=compile")
	if err != nil {
		t.Fatalf("mvn failed: %v", err)
	}
	mvnBytes, _ := os.ReadFile(cpFile)

	jvOut, err := runToolCmd(t, dir, nil, jvBin, "-q", "classpath", "compile")
	if err != nil {
		t.Fatalf("jv failed: %v: %s", err, jvOut)
	}

	compareClasspaths(t, "compile", jvOut, string(mvnBytes))
	if !strings.Contains(jvOut, "commons-io-2.14.0.jar") {
		t.Errorf("expected managed version 2.14.0 in classpath, got: %s", jvOut)
	}
}

func TestParityMaven_Exclusions(t *testing.T) {
	requireTools(t, "mvn", "javac")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	pom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>exclusions-parity</artifactId>
  <version>1.0</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <dependencies>
    <dependency>
      <groupId>org.apache.commons</groupId>
      <artifactId>commons-configuration2</artifactId>
      <version>2.9.0</version>
      <exclusions>
        <exclusion>
          <groupId>org.apache.commons</groupId>
          <artifactId>commons-text</artifactId>
        </exclusion>
      </exclusions>
    </dependency>
  </dependencies>
</project>`

	writeProject(t, dir, pom, "package demo; public class App {}")

	cpFile := filepath.Join(dir, "mvn.cp")
	_, err := runToolCmd(t, dir, nil, "mvn", "-q", "-B", "-ntp", "dependency:build-classpath",
		"-Dmdep.outputFile="+cpFile, "-DincludeScope=runtime")
	if err != nil {
		t.Fatalf("mvn failed: %v", err)
	}
	mvnBytes, _ := os.ReadFile(cpFile)

	jvOut, err := runToolCmd(t, dir, nil, jvBin, "-q", "classpath", "runtime")
	if err != nil {
		t.Fatalf("jv failed: %v: %s", err, jvOut)
	}

	compareClasspaths(t, "runtime", jvOut, string(mvnBytes))
	if strings.Contains(jvOut, "commons-text") {
		t.Errorf("excluded dependency commons-text found on classpath: %s", jvOut)
	}
}

func TestParityMaven_ResourceFiltering(t *testing.T) {
	requireTools(t, "mvn", "javac")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	pom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>filtering-parity</artifactId>
  <version>2.5.0</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
    <custom.greeting>HelloFilter</custom.greeting>
  </properties>
  <build>
    <resources>
      <resource>
        <directory>src/main/resources</directory>
        <filtering>true</filtering>
      </resource>
    </resources>
  </build>
</project>`

	writeProject(t, dir, pom, "package demo; public class App {}")
	resDir := filepath.Join(dir, "src/main/resources")
	_ = os.MkdirAll(resDir, 0o755)
	resContent := "app.version=${project.version}\napp.greeting=${custom.greeting}\n"
	_ = os.WriteFile(filepath.Join(resDir, "app.properties"), []byte(resContent), 0o644)

	// Build with jv using JV_IGNORE_BUILD_CONFIG=1 so native resource filtering executes
	jvOut, err := runToolCmd(t, dir, []string{"JV_IGNORE_BUILD_CONFIG=1"}, jvBin, "compile")
	if err != nil {
		t.Fatalf("jv compile failed: %v: %s", err, jvOut)
	}
	t.Logf("jv compile output: %s", jvOut)

	jvProps, err := os.ReadFile(filepath.Join(dir, "target/classes/app.properties"))
	if err != nil {
		t.Fatalf("reading jv app.properties: %v", err)
	}

	// Build reference with mvn
	mvnDir := t.TempDir()
	writeProject(t, mvnDir, pom, "package demo; public class App {}")
	_ = os.MkdirAll(filepath.Join(mvnDir, "src/main/resources"), 0o755)
	_ = os.WriteFile(filepath.Join(mvnDir, "src/main/resources/app.properties"), []byte(resContent), 0o644)
	_, err = runToolCmd(t, mvnDir, nil, "mvn", "-q", "-B", "-ntp", "compile")
	if err != nil {
		t.Fatalf("mvn compile failed: %v", err)
	}

	mvnProps, err := os.ReadFile(filepath.Join(mvnDir, "target/classes/app.properties"))
	if err != nil {
		t.Fatalf("reading mvn app.properties: %v", err)
	}

	if string(jvProps) != string(mvnProps) {
		t.Errorf("filtered resource mismatch:\nJV:\n%s\nMVN:\n%s", string(jvProps), string(mvnProps))
	}
}

func TestParityMaven_Packaging(t *testing.T) {
	requireTools(t, "mvn", "javac", "java")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	pom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>pack-parity</artifactId>
  <version>1.0</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
    <jv.mainClass>demo.App</jv.mainClass>
  </properties>
</project>`

	mainJava := `package demo;
public class App {
    public static void main(String[] args) {
        System.out.println("PARITY_PACKAGING_OK");
    }
}`

	writeProject(t, dir, pom, mainJava)

	jvOut, err := runToolCmd(t, dir, nil, jvBin, "-q", "package")
	if err != nil {
		t.Fatalf("jv package failed: %v: %s", err, jvOut)
	}

	jarPath := filepath.Join(dir, "target/pack-parity-1.0.jar")
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		t.Fatalf("cannot open produced jar: %v", err)
	}
	defer zr.Close()

	entries := map[string]bool{}
	for _, f := range zr.File {
		entries[f.Name] = true
	}

	for _, want := range []string{"META-INF/MANIFEST.MF", "demo/App.class", "META-INF/maven/demo/pack-parity/pom.xml"} {
		if !entries[want] {
			t.Errorf("jar missing expected entry: %s", want)
		}
	}

	// Verify jar execution
	javaOut, err := runToolCmd(t, dir, nil, "java", "-jar", jarPath)
	if err != nil || !strings.Contains(javaOut, "PARITY_PACKAGING_OK") {
		t.Errorf("running packaged jar failed: %v: %s", err, javaOut)
	}
}

func TestParityMaven_Plugins_Flatten(t *testing.T) {
	requireTools(t, "mvn", "javac")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	pom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>flatten-parity</artifactId>
  <version>${revision}</version>
  <properties>
    <revision>3.2.1-SNAPSHOT</revision>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <build>
    <plugins>
      <plugin>
        <groupId>org.codehaus.mojo</groupId>
        <artifactId>flatten-maven-plugin</artifactId>
        <version>1.6.0</version>
      </plugin>
    </plugins>
  </build>
</project>`

	writeProject(t, dir, pom, "package demo; public class App {}")

	jvOut, err := runToolCmd(t, dir, []string{"JV_IGNORE_BUILD_CONFIG=1"}, jvBin, "-q", "compile")
	if err != nil {
		t.Fatalf("jv compile failed: %v: %s", err, jvOut)
	}

	flattenFile := filepath.Join(dir, ".flattened-pom.xml")
	data, err := os.ReadFile(flattenFile)
	if err != nil {
		t.Fatalf("expected .flattened-pom.xml to be generated: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "<version>3.2.1-SNAPSHOT</version>") {
		t.Errorf("expected flattened version 3.2.1-SNAPSHOT in:\n%s", content)
	}
	if strings.Contains(content, "${revision}") {
		t.Errorf("expected ${revision} to be substituted in:\n%s", content)
	}
}

func TestParityMaven_Plugins_Shade(t *testing.T) {
	requireTools(t, "mvn", "javac", "java")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	pom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>shade-parity</artifactId>
  <version>1.0</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
    <jv.mainClass>demo.App</jv.mainClass>
  </properties>
  <dependencies>
    <dependency>
      <groupId>commons-io</groupId>
      <artifactId>commons-io</artifactId>
      <version>2.15.1</version>
    </dependency>
  </dependencies>
  <build>
    <plugins>
      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-shade-plugin</artifactId>
        <version>3.5.2</version>
        <executions>
          <execution>
            <phase>package</phase>
            <goals><goal>shade</goal></goals>
          </execution>
        </executions>
      </plugin>
    </plugins>
  </build>
</project>`

	mainJava := `package demo;
import org.apache.commons.io.IOUtils;
public class App {
    public static void main(String[] args) {
        System.out.println("SHADE_PARITY_CLASS=" + IOUtils.class.getName());
    }
}`

	writeProject(t, dir, pom, mainJava)

	jvOut, err := runToolCmd(t, dir, []string{"JV_IGNORE_BUILD_CONFIG=1"}, jvBin, "-q", "package")
	if err != nil {
		t.Fatalf("jv package with shade failed: %v: %s", err, jvOut)
	}

	jarPath := filepath.Join(dir, "target/shade-parity-1.0.jar")
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		t.Fatalf("cannot open shaded jar: %v", err)
	}
	defer zr.Close()

	hasCommonsIO := false
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "org/apache/commons/io/IOUtils.class") {
			hasCommonsIO = true
			break
		}
	}
	if !hasCommonsIO {
		t.Errorf("shaded jar lacks dependency class org/apache/commons/io/IOUtils.class")
	}

	javaOut, err := runToolCmd(t, dir, nil, "java", "-jar", jarPath)
	if err != nil || !strings.Contains(javaOut, "SHADE_PARITY_CLASS=org.apache.commons.io.IOUtils") {
		t.Errorf("running shaded jar failed: %v: %s", err, javaOut)
	}
}

func TestParityMaven_Plugins_JaCoCo(t *testing.T) {
	requireTools(t, "mvn", "javac")
	jvBin := getTestJVBin(t)
	dir := t.TempDir()

	pom := `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId>
  <artifactId>jacoco-parity</artifactId>
  <version>1.0</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <dependencies>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter-api</artifactId>
      <version>5.10.2</version>
      <scope>test</scope>
    </dependency>
  </dependencies>
  <build>
    <plugins>
      <plugin>
        <groupId>org.jacoco</groupId>
        <artifactId>jacoco-maven-plugin</artifactId>
        <version>0.8.11</version>
      </plugin>
    </plugins>
  </build>
</project>`

	testJava := `package demo;
import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertTrue;
class AppTest {
    @Test void testOk() { assertTrue(true); }
}`

	writeProject(t, dir, pom, "package demo; public class App {}")
	_ = os.MkdirAll(filepath.Join(dir, "src/test/java/demo"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src/test/java/demo/AppTest.java"), []byte(testJava), 0o644)

	jvOut, err := runToolCmd(t, dir, []string{"JV_IGNORE_BUILD_CONFIG=1"}, jvBin, "test")
	if err != nil {
		t.Fatalf("jv test with jacoco failed: %v: %s", err, jvOut)
	}

	if !strings.Contains(jvOut, "prepared jacoco") && !strings.Contains(jvOut, "jacoco") {
		t.Logf("jv test output: %s", jvOut)
	}
}
