// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func isRange(v string) bool { return strings.HasPrefix(v, "[") || strings.HasPrefix(v, "(") }

type bound struct {
	v        string
	inc, set bool
}
type rangeSet struct{ lo, hi bound }

func parseRanges(spec string) ([]rangeSet, error) {
	s := strings.TrimSpace(spec)
	var out []rangeSet
	for len(s) > 0 {
		end := strings.IndexAny(s, "])")
		if (s[0] != '[' && s[0] != '(') || end < 0 {
			return nil, fmt.Errorf("bad version range %q", spec)
		}
		loInc, hiInc := s[0] == '[', s[end] == ']'
		parts := strings.Split(s[1:end], ",")
		var rs rangeSet
		switch len(parts) {
		case 1:
			v := strings.TrimSpace(parts[0])
			rs = rangeSet{bound{v, true, true}, bound{v, true, true}}
		case 2:
			if v := strings.TrimSpace(parts[0]); v != "" {
				rs.lo = bound{v, loInc, true}
			}
			if v := strings.TrimSpace(parts[1]); v != "" {
				rs.hi = bound{v, hiInc, true}
			}
		default:
			return nil, fmt.Errorf("bad version range %q", spec)
		}
		out = append(out, rs)
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s[end+1:]), ","))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bad version range %q", spec)
	}
	return out, nil
}

func (r rangeSet) contains(v string) bool {
	if r.lo.set {
		if c := compareVersions(v, r.lo.v); c < 0 || (c == 0 && !r.lo.inc) {
			return false
		}
	}
	if r.hi.set {
		if c := compareVersions(v, r.hi.v); c > 0 || (c == 0 && !r.hi.inc) {
			return false
		}
	}
	return true
}

func (l *Loader) metadata(g, a string) ([]string, error) {
	dest := filepath.Join(l.Repo, filepath.FromSlash(groupPath(g)), a, "maven-metadata-jv.xml")
	if st, err := os.Stat(dest); err != nil || (!l.Offline && time.Since(st.ModTime()) > 24*time.Hour) {
		if l.Offline {
			return nil, fmt.Errorf("%w: metadata for %s:%s (offline mode)", ErrOffline, g, a)
		}
		rel := path.Join(groupPath(g), a, "maven-metadata.xml")
		derr := error(ErrNotFound)
		for _, b := range l.bases() {
			if derr = l.download(b, rel, dest, false); derr == nil {
				break
			}
			if !errors.Is(derr, ErrNotFound) {
				return nil, derr
			}
		}
		if derr != nil {
			if _, serr := os.Stat(dest); serr != nil {
				return nil, derr
			}
		}
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		return nil, err
	}
	var m struct {
		Versions []string `xml:"versioning>versions>version"`
	}
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m.Versions, nil
}

// resolveRange picks the highest published version inside a Maven version range.
func (l *Loader) resolveRange(g, a, spec string) (string, error) {
	sets, err := parseRanges(spec)
	if err != nil {
		return "", err
	}
	vers, err := l.metadata(g, a)
	if err != nil {
		return "", fmt.Errorf("version range %s for %s:%s: %w", spec, g, a, err)
	}
	best := ""
	for _, v := range vers {
		v = strings.TrimSpace(v)
		for _, s := range sets {
			if s.contains(v) && (best == "" || compareVersions(v, best) > 0) {
				best = v
			}
		}
	}
	if best == "" {
		return "", fmt.Errorf("no version of %s:%s satisfies %s", g, a, spec)
	}
	return best, nil
}
