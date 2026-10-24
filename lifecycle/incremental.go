// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FilesIn is an input descriptor that fingerprints mtime+size of files in Dir
// matching Glob (Ant-style; empty = all files).
type FilesIn struct {
	Dir  string
	Glob string
}

// Incremental provides skip-if-unchanged logic for executor outputs.
// A JSON state file stores the fingerprint of the inputs from the last run.
type Incremental struct {
	StateFile string // e.g. target/.jv/compile.state
}

// Changed returns true when any input differs from the last recorded fingerprint.
// inputs may be: string, []string, FilesIn, or any fmt.Stringer.
func (inc *Incremental) Changed(inputs ...any) (bool, error) {
	sig, err := fingerprint(inputs)
	if err != nil {
		return true, err
	}
	data, err := os.ReadFile(inc.StateFile)
	if err != nil {
		return true, nil // no state file → always changed
	}
	var st incState
	if json.Unmarshal(data, &st) != nil {
		return true, nil
	}
	return st.Sig != sig, nil
}

// Done records the current fingerprint as the last successful run.
func (inc *Incremental) Done(inputs ...any) error {
	sig, err := fingerprint(inputs)
	if err != nil {
		return err
	}
	return inc.writeState(incState{Sig: sig})
}

// RecordOutputs saves the list of files produced in the last run.
// It merges with any existing state so Sig is not overwritten.
func (inc *Incremental) RecordOutputs(paths []string) error {
	data, _ := os.ReadFile(inc.StateFile)
	var st incState
	json.Unmarshal(data, &st) //nolint:errcheck — blank state is fine
	st.Outputs = paths
	return inc.writeState(st)
}

// Outputs returns the file list from the last successful run.
// Returns nil, nil if no state exists.
func (inc *Incremental) Outputs() ([]string, error) {
	data, err := os.ReadFile(inc.StateFile)
	if err != nil {
		return nil, nil
	}
	var st incState
	if json.Unmarshal(data, &st) != nil {
		return nil, nil
	}
	return st.Outputs, nil
}

type incState struct {
	Sig     string   `json:"sig"`
	Outputs []string `json:"outputs,omitempty"`
}

func (inc *Incremental) writeState(st incState) error {
	if err := os.MkdirAll(filepath.Dir(inc.StateFile), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(inc.StateFile, data, 0o644)
}

func fingerprint(inputs []any) (string, error) {
	h := sha256.New()
	for _, inp := range inputs {
		switch v := inp.(type) {
		case string:
			fmt.Fprintf(h, "s:%s\n", v)
		case []string:
			s := append([]string(nil), v...)
			sort.Strings(s)
			for _, x := range s {
				fmt.Fprintf(h, "l:%s\n", x)
			}
		case FilesIn:
			if err := hashDir(h, v.Dir, v.Glob); err != nil {
				return "", err
			}
		default:
			fmt.Fprintf(h, "v:%v\n", v)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashDir(h interface{ Write([]byte) (int, error) }, dir, glob string) error {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if glob != "" && !antMatch(glob, rel) {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "f:%s:%d:%d\n", p, fi.Size(), fi.ModTime().UnixNano())
	}
	return nil
}

// antMatch is a minimal Ant-style glob matcher.
// Supported: **/ and /** and ** (any path segments), * (within one segment), literal text.
func antMatch(pat, s string) bool {
	for {
		if pat == "" {
			return s == ""
		}
		// ** anywhere (at end, or as /**) matches any remaining path.
		if pat == "**" || pat == "/**" {
			return true
		}
		// **/ at the start: match zero or more leading path segments.
		if strings.HasPrefix(pat, "**/") {
			if antMatch(pat[3:], s) {
				return true
			}
			i := strings.IndexByte(s, '/')
			if i < 0 {
				return false
			}
			s = s[i+1:]
			continue
		}
		// Find next * in pattern.
		star := strings.IndexByte(pat, '*')
		if star < 0 {
			return pat == s
		}
		// Consume the literal prefix before the first *.
		if !strings.HasPrefix(s, pat[:star]) {
			return false
		}
		s = s[star:]
		pat = pat[star:]
		// At this point pat starts with * or **.
		// If pat starts with **, treat it as matching any remaining path.
		if strings.HasPrefix(pat, "**") {
			rest := pat[2:] // what comes after **
			if rest == "" || rest == "/" {
				return true // /** or ** at end matches everything remaining
			}
			// **/suffix: try matching rest against every suffix of s
			if strings.HasPrefix(rest, "/") {
				rest = rest[1:]
			}
			for {
				if antMatch(rest, s) {
					return true
				}
				i := strings.IndexByte(s, '/')
				if i < 0 {
					return false
				}
				s = s[i+1:]
			}
		}
		// Single *: matches any characters except /
		pat = pat[1:]
		for i := 0; i <= len(s); i++ {
			if antMatch(pat, s[i:]) {
				return true
			}
			if i < len(s) && s[i] == '/' {
				break
			}
		}
		return false
	}
}
