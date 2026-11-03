// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package worker

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// The embedded worker runs on whatever JDK the user has, so its class files must not be newer than
// the oldest JDK jv supports (Java 17 = class file version 61). Regenerate with `make worker-jar`.
func TestEmbeddedServerJarRunsOnJava17(t *testing.T) {
	zr, err := zip.NewReader(bytes.NewReader(serverJar), int64(len(serverJar)))
	if err != nil {
		t.Fatalf("server.jar is not a valid jar: %v", err)
	}
	found := 0
	for _, f := range zr.File {
		if len(f.Name) < 6 || f.Name[len(f.Name)-6:] != ".class" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 8)
		_, err = io.ReadFull(rc, head)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		found++
		if major := binary.BigEndian.Uint16(head[6:8]); major > 61 {
			t.Errorf("%s has class file version %d; rebuild the jar with `make worker-jar` (--release 17)", f.Name, major)
		}
	}
	if found == 0 {
		t.Fatal("server.jar contains no classes")
	}
}
