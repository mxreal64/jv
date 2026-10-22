package resolver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jv-build/jv/config"
)

func snapshotMeta(stamp string) string {
	return `<metadata><versioning><snapshot><timestamp>` + stamp[strings.Index(stamp, "-")+1:strings.LastIndex(stamp, "-")] +
		`</timestamp><buildNumber>` + stamp[strings.LastIndex(stamp, "-")+1:] + `</buildNumber></snapshot><snapshotVersions>` +
		`<snapshotVersion><extension>jar</extension><value>` + stamp + `</value></snapshotVersion>` +
		`<snapshotVersion><extension>pom</extension><value>` + stamp + `</value></snapshotVersion></snapshotVersions></versioning></metadata>`
}

func TestRemoteSnapshot(t *testing.T) {
	var mu sync.Mutex
	files := map[string]string{}
	publish := func(stamp, content string) {
		mu.Lock()
		defer mu.Unlock()
		files["t/s/1.0-SNAPSHOT/maven-metadata.xml"] = snapshotMeta(stamp)
		files["t/s/1.0-SNAPSHOT/s-"+stamp+".jar"] = content
		files["t/s/1.0-SNAPSHOT/s-"+stamp+".pom"] = "<project/>"
	}
	var jarHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".jar") {
			jarHits.Add(1)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	repo := t.TempDir()
	newLoader := func() *Loader {
		s := &config.Settings{Mirrors: []config.Mirror{{ID: "m", URL: srv.URL, MirrorOf: "*"}}}
		return NewLoader(context.Background(), repo, s)
	}
	read := func(l *Loader) string {
		t.Helper()
		p, err := l.fetchFile("t", "s", "1.0-SNAPSHOT", "s-1.0-SNAPSHOT.jar", false)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	publish("1.0-20240101.101010-3", "build-3")
	if got := read(newLoader()); got != "build-3" {
		t.Fatalf("first fetch: %q", got)
	}
	if got := read(newLoader()); got != "build-3" || jarHits.Load() != 1 {
		t.Fatalf("a cached snapshot must not be re-downloaded without -U (got %q, %d downloads)", got, jarHits.Load())
	}

	l := newLoader()
	l.UpdateSnapshots = true
	if got := read(l); got != "build-3" || jarHits.Load() != 1 {
		t.Fatalf("-U with an unchanged build must not download again (got %q, %d downloads)", got, jarHits.Load())
	}
	publish("1.0-20240102.121212-4", "build-4")
	if got := read(l); got != "build-4" || jarHits.Load() != 2 {
		t.Fatalf("-U must pick up the newer build (got %q, %d downloads)", got, jarHits.Load())
	}
	if got := read(newLoader()); got != "build-4" {
		t.Fatalf("the cache must now hold build 4, got %q", got)
	}

	_, err := newLoader().fetchFile("t", "missing", "1.0-SNAPSHOT", "missing-1.0-SNAPSHOT.jar", false)
	if !errors.Is(err, ErrSnapshot) {
		t.Fatalf("want ErrSnapshot for an unpublished snapshot, got %v", err)
	}
	off := newLoader()
	off.Offline = true
	if _, err := off.fetchFile("t", "other", "1.0-SNAPSHOT", "other-1.0-SNAPSHOT.jar", false); !errors.Is(err, ErrOffline) {
		t.Fatalf("want ErrOffline, got %v", err)
	}
}

func TestSnapshotStampFallbacks(t *testing.T) {
	old := []byte(`<metadata><versioning><snapshot><timestamp>20240101.101010</timestamp><buildNumber>7</buildNumber></snapshot></versioning></metadata>`)
	if got := snapshotStamp(old, "2.0", "jar", ""); got != "2.0-20240101.101010-7" {
		t.Fatalf("got %q", got)
	}
	withCls := []byte(`<metadata><versioning><snapshotVersions><snapshotVersion><classifier>tests</classifier><extension>jar</extension><value>2.0-20240101.101010-7</value></snapshotVersion></snapshotVersions></versioning></metadata>`)
	if got := snapshotStamp(withCls, "2.0", "jar", "tests"); got != "2.0-20240101.101010-7" {
		t.Fatalf("classifier lookup: %q", got)
	}
	if got := snapshotStamp(withCls, "2.0", "jar", ""); got != "" {
		t.Fatalf("a plain jar must not match the tests classifier, got %q", got)
	}
}
