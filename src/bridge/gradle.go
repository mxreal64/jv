package bridge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DetectGradle checks if the directory or its ancestors is a Gradle project
// and returns whether Gradle is detected and the path to the executable to use.
func DetectGradle(dir string) (bool, string) {
	curr := filepath.Clean(dir)
	var wrapperPath string
	hasGradleBuild := false

	for {
		for _, name := range []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"} {
			if _, err := os.Stat(filepath.Join(curr, name)); err == nil {
				hasGradleBuild = true
				break
			}
		}

		wrapperName := "gradlew"
		if runtime.GOOS == "windows" {
			wrapperName = "gradlew.bat"
		}
		w := filepath.Join(curr, wrapperName)
		if st, err := os.Stat(w); err == nil && !st.IsDir() {
			wrapperPath = w
			hasGradleBuild = true
			break
		}

		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}

	if !hasGradleBuild {
		return false, ""
	}
	if wrapperPath != "" {
		return true, wrapperPath
	}
	if gradleBin, err := exec.LookPath("gradle"); err == nil {
		return true, gradleBin
	}
	return true, "gradle"
}

// TranslateMavenArgs maps standard Maven lifecycle goals and flags to Gradle equivalents.
func TranslateMavenArgs(args []string) []string {
	var out []string
	var tasks []string
	var subproject string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "--offline":
			out = append(out, "--offline")
		case a == "-q" || a == "--quiet":
			out = append(out, "--quiet")
		case a == "--verbose" || a == "-X":
			out = append(out, "--info")
		case a == "-DskipTests" || a == "-DskipTests=true" || a == "-Dmaven.test.skip=true" || a == "-Dmaven.test.skip":
			out = append(out, "-x", "test")
		case strings.HasPrefix(a, "-D") && len(a) > 2:
			out = append(out, a)
		case a == "-D" && i+1 < len(args):
			i++
			out = append(out, "-D"+args[i])
		case strings.HasPrefix(a, "-P") && len(a) > 2:
			out = append(out, "-P"+a[2:])
		case a == "-P" && i+1 < len(args):
			i++
			out = append(out, "-P"+args[i])
		case (a == "-pl" || a == "--projects") && i+1 < len(args):
			i++
			subproject = strings.TrimPrefix(args[i], ":")
		case strings.HasPrefix(a, "-"):
			out = append(out, a)
		default:
			switch a {
			case "clean":
				tasks = append(tasks, "clean")
			case "compile":
				tasks = append(tasks, "classes")
			case "test":
				tasks = append(tasks, "test")
			case "package":
				tasks = append(tasks, "build") // like Maven's package, this runs the tests
			case "install":
				tasks = append(tasks, "publishToMavenLocal")
			case "tree", "dependencies":
				tasks = append(tasks, "dependencies")
			default:
				tasks = append(tasks, a)
			}
		}
	}

	if subproject != "" && len(tasks) > 0 {
		var scopedTasks []string
		for _, t := range tasks {
			scopedTasks = append(scopedTasks, fmt.Sprintf(":%s:%s", subproject, t))
		}
		tasks = scopedTasks
	}

	return append(out, tasks...)
}

// FallbackToGradle runs Gradle (using gradlew if present) and returns its exit code.
func FallbackToGradle(args []string) int {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	detected, cmdPath := DetectGradle(root)
	if !detected || cmdPath == "" {
		fmt.Fprintf(os.Stderr, "[jv] no Gradle project or executable found\n")
		return 127
	}

	gradleArgs := TranslateMavenArgs(args)
	cmd := exec.Command(cmdPath, gradleArgs...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "[jv] could not run %s: %v\n", cmdPath, err)
		return 127
	}
	return 0
}
