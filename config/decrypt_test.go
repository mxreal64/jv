// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package config

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// plexusEncrypt is the inverse of plexusDecrypt, used to check the layout round-trips.
func plexusEncrypt(t *testing.T, plain, passphrase string) string {
	t.Helper()
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	h := sha256.New()
	h.Write([]byte(passphrase))
	h.Write(salt)
	kiv := h.Sum(nil)
	block, err := aes.NewCipher(kiv[:16])
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	pt := append([]byte(plain), bytes.Repeat([]byte{byte(pad)}, pad)...)
	ct := make([]byte, len(pt))
	cipher.NewCBCEncrypter(block, kiv[16:32]).CryptBlocks(ct, pt)
	padLen := 0
	if m := (len(salt) + 1 + len(ct)) % aes.BlockSize; m > 0 {
		padLen = aes.BlockSize - m
	}
	raw := append(append(append([]byte{}, salt...), byte(padLen)), ct...)
	raw = append(raw, make([]byte, padLen)...)
	return "{" + base64.StdEncoding.EncodeToString(raw) + "}"
}

func TestDecryptPasswordRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".m2"), 0o755); err != nil {
		t.Fatal(err)
	}
	master := "s3cret-master"
	sec := "<settingsSecurity><master>" + plexusEncrypt(t, master, masterPassphrase) + "</master></settingsSecurity>"
	if err := os.WriteFile(filepath.Join(home, ".m2", "settings-security.xml"), []byte(sec), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, plain := range []string{"p@ss w0rd!", "x", "exactly-16-chars!", "a much longer password with more than two AES blocks of text"} {
		got, err := DecryptPassword(plexusEncrypt(t, plain, master))
		if err != nil {
			t.Fatalf("%q: %v", plain, err)
		}
		if got != plain {
			t.Errorf("got %q, want %q", got, plain)
		}
	}
	if got, err := DecryptPassword("plain-text"); err != nil || got != "plain-text" {
		t.Errorf("unencrypted password must pass through, got %q, %v", got, err)
	}
}

func TestDecryptPasswordWithoutMaster(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := DecryptPassword("{AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=}"); err == nil {
		t.Error("expected an error when settings-security.xml is missing")
	}
}

func TestUndecorate(t *testing.T) {
	if got, ok := undecorate("some comment {abc123} trailing"); !ok || got != "abc123" {
		t.Errorf("undecorate = %q, %v", got, ok)
	}
	if _, ok := undecorate("no braces"); ok {
		t.Error("expected failure without braces")
	}
}
