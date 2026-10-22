// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package resolver

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

type snapMeta struct {
	Timestamp   string `xml:"versioning>snapshot>timestamp"`
	BuildNumber string `xml:"versioning>snapshot>buildNumber"`
	Versions    []struct {
		Classifier string `xml:"classifier"`
		Extension  string `xml:"extension"`
		Value      string `xml:"value"`
	} `xml:"versioning>snapshotVersions>snapshotVersion"`
}

// snapshotStamp returns the timestamped version ("1.0-20240101.123456-3") that a
// -SNAPSHOT directory's maven-metadata.xml publishes for the given extension/classifier.
func snapshotStamp(data []byte, base, ext, cls string) string {
	var m snapMeta
	if xml.Unmarshal(data, &m) != nil {
		return ""
	}
	for _, sv := range m.Versions {
		v := strings.TrimSpace(sv.Value)
		if v != "" && strings.TrimSpace(sv.Extension) == ext && strings.TrimSpace(sv.Classifier) == cls {
			return v
		}
	}
	ts, bn := strings.TrimSpace(m.Timestamp), strings.TrimSpace(m.BuildNumber)
	if ts != "" && bn != "" {
		return base + "-" + ts + "-" + bn
	}
	return ""
}

// fetchBytes downloads a small text resource (maven-metadata.xml) without caching it.
func (l *Loader) fetchBytes(b repoRef, rel string) ([]byte, error) {
	l.sem <- struct{}{}
	defer func() { <-l.sem }()
	ctx, cancel := context.WithTimeout(l.ctx, 60*time.Second)
	defer cancel()
	url := strings.TrimRight(b.URL, "/") + "/" + rel
	if l.Verbose {
		fmt.Fprintf(os.Stderr, "[jv] GET %s\n", url)
	}
	resp, err := l.get(ctx, b, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNotFound, url)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// fetchSnapshot resolves g:a:v-SNAPSHOT/file against the configured repositories: it reads the
// version directory's maven-metadata.xml, downloads the newest timestamped file and stores it
// under the plain -SNAPSHOT name (as Maven does). A marker file remembers which build is
// cached, so -U only re-downloads when the repository has a newer build. Repositories using
// the old non-unique layout (files named ...-SNAPSHOT.jar) are supported too, and a copy
// installed locally by `jv install`/`mvn install` is kept when no repository has the artifact.
func (l *Loader) fetchSnapshot(g, a, v, file, dest string, verify bool) (string, error) {
	dirRel := path.Join(groupPath(g), a, v)
	ext := strings.TrimPrefix(path.Ext(file), ".")
	stem := strings.TrimSuffix(file, "."+ext)
	cls := strings.TrimPrefix(strings.TrimPrefix(stem, a+"-"+v), "-")
	base := strings.TrimSuffix(v, "-SNAPSHOT")
	marker := dest + ".jv-snapshot"
	for _, b := range l.bases() {
		data, err := l.fetchBytes(b, dirRel+"/maven-metadata.xml")
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				return "", err
			}
			derr := l.download(b, dirRel+"/"+file, dest, verify) // non-unique snapshot layout
			if derr == nil {
				return dest, nil
			}
			if !errors.Is(derr, ErrNotFound) {
				return "", derr
			}
			continue
		}
		stamp := snapshotStamp(data, base, ext, cls)
		if stamp == "" {
			continue
		}
		if old, e := os.ReadFile(marker); e == nil && string(old) == stamp {
			if st, e := os.Stat(dest); e == nil && st.Size() > 0 {
				return dest, nil
			}
		}
		name := a + "-" + stamp
		if cls != "" {
			name += "-" + cls
		}
		if derr := l.download(b, dirRel+"/"+name+"."+ext, dest, verify); derr != nil {
			if errors.Is(derr, ErrNotFound) {
				continue
			}
			return "", derr
		}
		_ = os.WriteFile(marker, []byte(stamp), 0o644)
		return dest, nil
	}
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return dest, nil
	}
	return "", fmt.Errorf("%w: %s/%s (not published to any configured repository; run `jv install` or `mvn install` in its project)", ErrSnapshot, dirRel, file)
}
