// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package config

// Maven password encryption (settings-security.xml), as implemented by plexus-cipher:
//
//	blob   = "{" + base64(salt[8] + padLen[1] + AES-CBC-PKCS5(plain) + padding[padLen]) + "}"
//	key||iv = SHA-256(passphrase || salt)   (first 16 bytes the AES-128 key, next 16 the IV)
//
// The master password in ~/.m2/settings-security.xml is itself encrypted with the fixed passphrase
// "settings.security"; server passwords are encrypted with the (decrypted) master password.
// A second, older layout (IV prefix, no salt) is tried as a fallback.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// masterPassphrase is the fixed passphrase that protects the master password.
const masterPassphrase = "settings.security"

type securitySettings struct {
	Master     string `xml:"master"`
	Relocation string `xml:"relocation"`
}

type masterResult struct {
	pw  string
	err error
}

var masterCache sync.Map // settings-security.xml path -> masterResult

// loadMasterPassword returns the decrypted master password, or "" if there is none.
func loadMasterPassword() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".m2", "settings-security.xml")
	if v, ok := masterCache.Load(path); ok {
		r := v.(masterResult)
		return r.pw, r.err
	}
	pw, err := readMaster(path, 0)
	masterCache.Store(path, masterResult{pw, err})
	return pw, err
}

func readMaster(path string, depth int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var ss securitySettings
	if err := xml.Unmarshal(data, &ss); err != nil {
		return "", fmt.Errorf("settings-security.xml: %w", err)
	}
	if rel := strings.TrimSpace(ss.Relocation); rel != "" && depth < 1 {
		return readMaster(expand(rel), depth+1)
	}
	master := strings.TrimSpace(ss.Master)
	if master == "" {
		return "", nil
	}
	if !IsEncrypted(master) {
		return master, nil
	}
	return decryptBlob(master, masterPassphrase)
}

// undecorate returns the text between the braces of a "{...}" blob (a comment may surround it).
func undecorate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i+1 {
		return "", false
	}
	return s[i+1 : j], true
}

// decryptBlob tries the plexus-cipher layout first, then the legacy one.
func decryptBlob(blob, passphrase string) (string, error) {
	pt, err := plexusDecrypt(blob, passphrase)
	if err == nil {
		return pt, nil
	}
	if legacy, lerr := legacyDecrypt(blob, passphrase); lerr == nil {
		return legacy, nil
	}
	return "", err
}

func unpad(pt []byte) ([]byte, error) {
	if len(pt) == 0 {
		return nil, errors.New("empty plaintext")
	}
	n := int(pt[len(pt)-1])
	if n == 0 || n > aes.BlockSize || n > len(pt) {
		return nil, errors.New("invalid padding (wrong master password?)")
	}
	for _, b := range pt[len(pt)-n:] {
		if int(b) != n {
			return nil, errors.New("invalid padding (wrong master password?)")
		}
	}
	return pt[:len(pt)-n], nil
}

func plexusDecrypt(blob, passphrase string) (string, error) {
	inner, ok := undecorate(blob)
	if !ok {
		return "", errors.New("not a Maven encrypted blob")
	}
	raw, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", fmt.Errorf("base64: %w", err)
	}
	const saltLen = 8
	if len(raw) < saltLen+1+aes.BlockSize {
		return "", errors.New("encrypted blob too short")
	}
	salt, padLen := raw[:saltLen], int(raw[saltLen])
	end := len(raw) - padLen
	if padLen > aes.BlockSize || end <= saltLen+1 {
		return "", errors.New("invalid padding length")
	}
	ct := raw[saltLen+1 : end]
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return "", errors.New("ciphertext length is not a multiple of the AES block size")
	}
	h := sha256.New()
	h.Write([]byte(passphrase))
	h.Write(salt)
	kiv := h.Sum(nil)
	block, err := aes.NewCipher(kiv[:16])
	if err != nil {
		return "", err
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, kiv[16:32]).CryptBlocks(pt, ct)
	pt, err = unpad(pt)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// legacyDecrypt handles blobs of the form IV[16] + ciphertext with key = SHA-256(passphrase)[:16].
func legacyDecrypt(blob, passphrase string) (string, error) {
	inner, ok := undecorate(blob)
	if !ok {
		return "", errors.New("not a Maven encrypted blob")
	}
	raw, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", fmt.Errorf("base64: %w", err)
	}
	if len(raw) < 2*aes.BlockSize || (len(raw)-aes.BlockSize)%aes.BlockSize != 0 {
		return "", errors.New("blob has the wrong length")
	}
	sum := sha256.Sum256([]byte(passphrase))
	block, err := aes.NewCipher(sum[:16])
	if err != nil {
		return "", err
	}
	iv, ct := raw[:aes.BlockSize], raw[aes.BlockSize:]
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
	pt, err = unpad(pt)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// DecryptPassword decrypts a Maven {...} password using the master password from
// ~/.m2/settings-security.xml. A password that is not encrypted is returned unchanged.
func DecryptPassword(blob string) (string, error) {
	if !IsEncrypted(blob) {
		return blob, nil
	}
	master, err := loadMasterPassword()
	if err != nil {
		return "", fmt.Errorf("reading master password: %w", err)
	}
	if master == "" {
		return "", errors.New("no master password: ~/.m2/settings-security.xml is missing or empty")
	}
	return decryptBlob(blob, master)
}
