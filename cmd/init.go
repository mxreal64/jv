// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func InitProject(dir, group, artifact, version, projType string) error {
	if group == "" {
		group = "com.example"
	}
	if artifact == "" {
		artifact = "myapp"
	}
	if version == "" {
		version = "1.0-SNAPSHOT"
	}

	pkgDir := strings.ReplaceAll(group, ".", "/")
	srcDir := filepath.Join(dir, "src/main/java", pkgDir)
	testDir := filepath.Join(dir, "src/test/java", pkgDir)

	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		return err
	}

	pomContent := fmt.Sprintf(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>%s</groupId>
  <artifactId>%s</artifactId>
  <version>%s</version>
  <properties>
    <maven.compiler.release>17</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <dependencies>
    <dependency>
      <groupId>org.junit.jupiter</groupId>
      <artifactId>junit-jupiter</artifactId>
      <version>5.10.2</version>
      <scope>test</scope>
    </dependency>
  </dependencies>
</project>
`, group, artifact, version)

	if projType == "spring-boot" {
		pomContent = fmt.Sprintf(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>%s</groupId>
  <artifactId>%s</artifactId>
  <version>%s</version>
  <build>
    <plugins>
      <plugin>
        <groupId>org.springframework.boot</groupId>
        <artifactId>spring-boot-maven-plugin</artifactId>
        <version>3.2.2</version>
      </plugin>
    </plugins>
  </build>
</project>
`, group, artifact, version)
	}

	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(pomContent), 0o644); err != nil {
		return err
	}

	appJava := fmt.Sprintf(`package %s;

public class App {
    public static String greet() {
        return "Hello World";
    }
    public static void main(String[] args) {
        System.out.println(greet());
    }
}
`, group)
	if err := os.WriteFile(filepath.Join(srcDir, "App.java"), []byte(appJava), 0o644); err != nil {
		return err
	}

	appTest := fmt.Sprintf(`package %s;

import org.junit.jupiter.api.Test;
import static org.junit.jupiter.api.Assertions.assertEquals;

public class AppTest {
    @Test
    public void testGreet() {
        assertEquals("Hello World", App.greet());
    }
}
`, group)
	if err := os.WriteFile(filepath.Join(testDir, "AppTest.java"), []byte(appTest), 0o644); err != nil {
		return err
	}

	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("target/\n.jv/\n"), 0o644)
	return nil
}
