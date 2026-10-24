// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package springboot

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Repackage transforms a standard project jar into a Spring Boot executable fat jar.
func Repackage(originalJar string, runtimeCP []string, mainClass string, springBootVersion string, outJar string) error {
	// Find spring-boot-loader jar in runtimeCP if available
	var loaderJarPath string
	for _, cp := range runtimeCP {
		base := filepath.Base(cp)
		if strings.HasPrefix(base, "spring-boot-loader-") && strings.HasSuffix(base, ".jar") {
			loaderJarPath = cp
			break
		}
	}

	// Rename original jar to original-{name}.jar if outJar == originalJar
	stageOriginal := originalJar
	if originalJar == outJar {
		dir := filepath.Dir(originalJar)
		base := filepath.Base(originalJar)
		backup := filepath.Join(dir, "original-"+base)
		if err := os.Rename(originalJar, backup); err != nil {
			return fmt.Errorf("renaming original jar: %w", err)
		}
		stageOriginal = backup
	}

	origR, err := zip.OpenReader(stageOriginal)
	if err != nil {
		return fmt.Errorf("reading original jar: %w", err)
	}
	defer origR.Close()

	tmpOut := outJar + ".tmp"
	outFile, err := os.Create(tmpOut)
	if err != nil {
		return err
	}
	defer func() {
		outFile.Close()
		_ = os.Remove(tmpOut)
	}()

	zw := zip.NewWriter(outFile)

	// Determine JarLauncher main class based on springBootVersion
	launcherClass := "org.springframework.boot.loader.launch.JarLauncher"
	if springBootVersion != "" && (strings.HasPrefix(springBootVersion, "1.") || strings.HasPrefix(springBootVersion, "2.") || strings.HasPrefix(springBootVersion, "3.0.") || strings.HasPrefix(springBootVersion, "3.1.")) {
		launcherClass = "org.springframework.boot.loader.JarLauncher"
	}

	// Write META-INF/MANIFEST.MF
	manifestBuf := bytes.NewBuffer(nil)
	manifestBuf.WriteString("Manifest-Version: 1.0\r\n")
	manifestBuf.WriteString(fmt.Sprintf("Main-Class: %s\r\n", launcherClass))
	if mainClass != "" {
		manifestBuf.WriteString(fmt.Sprintf("Start-Class: %s\r\n", mainClass))
	}
	if springBootVersion != "" {
		manifestBuf.WriteString(fmt.Sprintf("Spring-Boot-Version: %s\r\n", springBootVersion))
	}
	manifestBuf.WriteString("Spring-Boot-Classes: BOOT-INF/classes/\r\n")
	manifestBuf.WriteString("Spring-Boot-Lib: BOOT-INF/lib/\r\n")
	manifestBuf.WriteString("Spring-Boot-Classpath-Index: BOOT-INF/classpath.idx\r\n")
	manifestBuf.WriteString("Spring-Boot-Layers-Index: BOOT-INF/layers.idx\r\n\r\n")

	mw, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		return err
	}
	if _, err := mw.Write(manifestBuf.Bytes()); err != nil {
		return err
	}

	// Copy original jar entries into BOOT-INF/classes/ (skip original META-INF/MANIFEST.MF)
	for _, f := range origR.File {
		if f.Name == "META-INF/MANIFEST.MF" {
			continue
		}
		targetName := "BOOT-INF/classes/" + f.Name
		w, err := zw.Create(targetName)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if err != nil {
			return err
		}
	}

	// Copy runtime dependencies into BOOT-INF/lib/
	var libNames []string
	for _, cp := range runtimeCP {
		if !strings.HasSuffix(cp, ".jar") {
			continue
		}
		fi, err := os.Stat(cp)
		if err != nil || fi.IsDir() {
			continue
		}
		base := filepath.Base(cp)
		libNames = append(libNames, base)

		libTarget := "BOOT-INF/lib/" + base
		w, err := zw.Create(libTarget)
		if err != nil {
			return err
		}
		srcF, err := os.Open(cp)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, srcF)
		srcF.Close()
		if err != nil {
			return err
		}
	}

	// Write BOOT-INF/classpath.idx
	var cpIdxBuf bytes.Buffer
	for _, lib := range libNames {
		cpIdxBuf.WriteString("- \"BOOT-INF/lib/" + lib + "\"\n")
	}
	cpW, err := zw.Create("BOOT-INF/classpath.idx")
	if err != nil {
		return err
	}
	if _, err := cpW.Write(cpIdxBuf.Bytes()); err != nil {
		return err
	}

	// Write BOOT-INF/layers.idx
	var layersIdxBuf bytes.Buffer
	layersIdxBuf.WriteString("- \"dependencies\":\n")
	for _, lib := range libNames {
		layersIdxBuf.WriteString("  - \"BOOT-INF/lib/" + lib + "\"\n")
	}
	layersIdxBuf.WriteString("- \"application\":\n")
	layersIdxBuf.WriteString("  - \"BOOT-INF/classes/\"\n")
	layersIdxBuf.WriteString("  - \"BOOT-INF/classpath.idx\"\n")
	layersIdxBuf.WriteString("  - \"BOOT-INF/layers.idx\"\n")

	layerW, err := zw.Create("BOOT-INF/layers.idx")
	if err != nil {
		return err
	}
	if _, err := layerW.Write(layersIdxBuf.Bytes()); err != nil {
		return err
	}

	// Extract org/springframework/boot/loader/ classes into root if loader jar exists
	if loaderJarPath != "" {
		if lzr, err := zip.OpenReader(loaderJarPath); err == nil {
			for _, f := range lzr.File {
				if strings.HasPrefix(f.Name, "org/springframework/boot/loader/") {
					w, err := zw.Create(f.Name)
					if err != nil {
						lzr.Close()
						return err
					}
					rc, err := f.Open()
					if err != nil {
						lzr.Close()
						return err
					}
					_, err = io.Copy(w, rc)
					rc.Close()
					if err != nil {
						lzr.Close()
						return err
					}
				}
			}
			lzr.Close()
		}
	}

	if err := zw.Close(); err != nil {
		return err
	}
	if err := outFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpOut, outJar)
}
