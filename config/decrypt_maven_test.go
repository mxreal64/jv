// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package config

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var blobRe = regexp.MustCompile(`\{[A-Za-z0-9+/=]+\}`)

// mvnEncrypt runs the real Maven to encrypt secret (flag is --encrypt-master-password or
// --encrypt-password) with home as the user's home directory.
func mvnEncrypt(t *testing.T, home, flag, secret string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "mvn", flag, secret)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "MAVEN_OPTS=-Duser.home="+home, "HOME="+home, "USERPROFILE="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mvn %s failed: %v\n%s", flag, err, out)
	}
	found := blobRe.FindAll(out, -1)
	if len(found) == 0 {
		t.Fatalf("no {...} blob in the output of mvn %s:\n%s", flag, out)
	}
	return string(found[len(found)-1])
}

// TestDecryptAgainstRealMaven checks jv's decryption against blobs produced by the installed
// Maven. It is skipped when mvn is not on the PATH. A failure here means jv's cipher layout does not
// match that Maven version; the message includes the shape of the blob to make that diagnosable.
func TestDecryptAgainstRealMaven(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".m2"), 0o755); err != nil {
		t.Fatal(err)
	}

	master := "correct horse battery staple"
	encMaster := mvnEncrypt(t, home, "--encrypt-master-password", master)
	sec := "<settingsSecurity><master>" + encMaster + "</master></settingsSecurity>"
	if err := os.WriteFile(filepath.Join(home, ".m2", "settings-security.xml"), []byte(sec), 0o644); err != nil {
		t.Fatal(err)
	}

	shape := func(blob string) string {
		inner, _ := undecorate(blob)
		raw, err := base64.StdEncoding.DecodeString(inner)
		if err != nil {
			return "not base64: " + err.Error()
		}
		return "decoded length " + strconv.Itoa(len(raw))
	}

	if got, err := loadMasterPassword(); err != nil || got != master {
		t.Fatalf("master password: got %q, err %v (mvn blob: %s)", got, err, shape(encMaster))
	}
	for _, plain := range []string{"hunter2", "p@ss w0rd! {braces}", strings.Repeat("long-", 12)} {
		enc := mvnEncrypt(t, home, "--encrypt-password", plain)
		got, err := DecryptPassword(enc)
		if err != nil {
			t.Fatalf("%q: %v (mvn blob: %s)", plain, err, shape(enc))
		}
		if got != plain {
			t.Errorf("got %q, want %q", got, plain)
		}
	}
}
