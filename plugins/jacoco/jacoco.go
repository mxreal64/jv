// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package jacoco

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mxreal64/jv/lifecycle"
)

// PrepareAgent extracts jacocoagent.jar from the jacoco agent jar in compile/test CP and configures argLine.
func PrepareAgent(bc *lifecycle.BuildContext) error {
	agentJarDst := filepath.Join(bc.TargetDir, "jacoco-agent.jar")
	if _, err := os.Stat(agentJarDst); os.IsNotExist(err) {
		// Look for org.jacoco.agent in compile or test classpath
		var foundSourceJar string
		for _, cp := range append(bc.CompileCP, bc.TestCP...) {
			if strings.Contains(cp, "org.jacoco.agent") && strings.HasSuffix(cp, ".jar") {
				foundSourceJar = cp
				break
			}
		}

		if foundSourceJar != "" {
			zr, err := zip.OpenReader(foundSourceJar)
			if err == nil {
				defer zr.Close()
				for _, f := range zr.File {
					if f.Name == "jacocoagent.jar" {
						if err := os.MkdirAll(bc.TargetDir, 0o755); err != nil {
							return err
						}
						rc, err := f.Open()
						if err != nil {
							return err
						}
						outF, err := os.Create(agentJarDst)
						if err != nil {
							rc.Close()
							return err
						}
						_, _ = io.Copy(outF, rc)
						outF.Close()
						rc.Close()
						break
					}
				}
			}
		}
	}

	execPath := filepath.Join(bc.TargetDir, "jacoco.exec")
	agentArg := fmt.Sprintf("-javaagent:%s=destfile=%s", agentJarDst, execPath)
	curArgLine := bc.Props["argLine"]
	if curArgLine != "" {
		bc.Props["argLine"] = curArgLine + " " + agentArg
	} else {
		bc.Props["argLine"] = agentArg
	}
	return nil
}

// GenerateReport runs the Jacoco CLI reporter if jacoco.exec exists.
func GenerateReport(ctx context.Context, bc lifecycle.BuildContext) error {
	execPath := filepath.Join(bc.TargetDir, "jacoco.exec")
	if _, err := os.Stat(execPath); err != nil {
		return nil // skip silently if no jacoco.exec
	}

	// Look for org.jacoco.cli jar in classpath or local repo
	var cliJar string
	for _, cp := range append(bc.CompileCP, bc.TestCP...) {
		if strings.Contains(cp, "org.jacoco.cli") && strings.HasSuffix(cp, ".jar") {
			cliJar = cp
			break
		}
	}
	if cliJar == "" {
		return nil
	}

	htmlDir := filepath.Join(bc.TargetDir, "site", "jacoco")
	xmlFile := filepath.Join(htmlDir, "jacoco.xml")
	_ = os.MkdirAll(htmlDir, 0o755)

	args := []string{
		"-jar", cliJar,
		"report", execPath,
		"--classfiles", bc.ClassesDir,
		"--sourcefiles", "src/main/java",
		"--html", htmlDir,
		"--xml", xmlFile,
	}

	cmd := exec.CommandContext(ctx, "java", args...)
	cmd.Dir = bc.Dir
	_ = cmd.Run()
	return nil
}
