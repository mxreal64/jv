// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mxreal64/jv/config"
	"github.com/mxreal64/jv/pom"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrSnapshot  = errors.New("snapshot not available")
	ErrOffline   = errors.New("not available offline")
	errPermanent = errors.New("permanent error")
)

const centralURL = "https://repo1.maven.org/maven2"

// repoRef is a repository to try. Trusted repositories (Central, settings.xml mirrors and
// repositories declared by the project's own POMs) may receive credentials; repositories
// that only a dependency's POM declared never do.
type repoRef struct {
	ID, URL string
	Trusted bool
}

// stallTimeout aborts a download that makes no progress for this long (there is no
// overall limit, so very large artifacts on slow links still complete).
const stallTimeout = 60 * time.Second

type idleReader struct {
	r io.Reader
	t *time.Timer
	d time.Duration
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	i.t.Reset(i.d)
	return n, err
}

// insecureRemote reports whether raw is a plain-http URL to a non-local host.
func insecureRemote(raw string) bool {
	u, err := neturl.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return false
	}
	return true
}

type rawEntry struct {
	once sync.Once
	r    *raw
	err  error
}

type effEntry struct {
	once sync.Once
	e    *Effective
	err  error
}

// Loader fetches POMs/jars into a Maven-compatible local repository and
// builds effective models (parents, properties, dependencyManagement, BOM imports).
type Loader struct {
	Repo       string
	Settings   *config.Settings
	Offline    bool
	Verbose    bool
	Quiet      bool
	ProfileIDs map[string]bool   // -P ids ("!id" disables)
	UserProps  map[string]string // -D properties

	UpdateSnapshots bool // -U: re-check remote SNAPSHOTs even when a copy is cached

	ctx    context.Context
	mods   map[string]*Module
	client *http.Client
	sem    chan struct{}

	mu    sync.Mutex
	raws  map[string]*rawEntry
	effs  map[string]*effEntry
	extra []repoRef

	warned   sync.Map
	envCache map[string]string // os.Environ snapshot taken once at construction
}

func NewLoader(ctx context.Context, repo string, s *config.Settings) *Loader {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 32
	tr.ResponseHeaderTimeout = 60 * time.Second
	if s != nil {
		if pf := s.ProxyFunc(); pf != nil {
			tr.Proxy = pf
		}
	}
	envCache := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			envCache["env."+kv[:i]] = kv[i+1:]
		}
	}
	return &Loader{
		Repo: repo, Settings: s, ctx: ctx, mods: map[string]*Module{},
		client:   &http.Client{Transport: tr},
		sem:      make(chan struct{}, 24),
		raws:     map[string]*rawEntry{},
		effs:     map[string]*effEntry{},
		envCache: envCache,
	}
}

// AddSettingsRepos registers repositories declared by active settings.xml profiles. They are
// trusted (they may receive the credentials configured for their id).
func (l *Loader) AddSettingsRepos(rs []config.Repo) {
	var out []pom.Repository
	for _, r := range rs {
		out = append(out, pom.Repository{ID: r.ID, URL: r.URL})
	}
	l.addRepos(out, true)
}

func (l *Loader) warnf(format string, a ...any) {
	if l.Quiet {
		return
	}
	fmt.Fprintf(os.Stderr, "[jv] warning: "+format+"\n", a...)
}

func (l *Loader) addRepos(rs []pom.Repository, trusted bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range rs {
		if r.URL == "" || strings.Contains(r.URL, "${") {
			continue
		}
		dup := false
		for i, e := range l.extra {
			if e.URL == r.URL {
				dup = true
				if trusted {
					l.extra[i].Trusted = true
				}
			}
		}
		if !dup {
			l.extra = append(l.extra, repoRef{r.ID, r.URL, trusted})
		}
	}
}

// bases lists repositories to try in order (Central, then POM-declared ones), after mirroring.
func (l *Loader) bases() []repoRef {
	l.mu.Lock()
	cands := append([]repoRef{{"central", centralURL, true}}, l.extra...)
	l.mu.Unlock()
	var out []repoRef
	seen := map[string]bool{}
	for _, c := range cands {
		if m, ok := l.Settings.MirrorFor(c.ID); ok {
			c = repoRef{m.ID, m.URL, true}
		} else if insecureRemote(c.URL) { // like Maven 3.8.1+, never use plain-http repositories from POMs
			if _, seen := l.warned.LoadOrStore(c.URL, true); !seen {
				l.warnf("ignoring insecure (http) repository %s declared in a POM", c.URL)
			}
			continue
		}
		if !seen[c.URL] {
			seen[c.URL] = true
			out = append(out, c)
		}
	}
	return out
}

func groupPath(g string) string { return strings.ReplaceAll(g, ".", "/") }

// fetchFile returns the local path of g:a:v/file, downloading it if absent.
func (l *Loader) fetchFile(g, a, v, file string, verify bool) (string, error) {
	dest := filepath.Join(l.Repo, filepath.FromSlash(groupPath(g)), a, v, file)
	snapshot := strings.HasSuffix(v, "-SNAPSHOT")
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 && !(snapshot && l.UpdateSnapshots && !l.Offline) {
		return dest, nil
	}
	rel := path.Join(groupPath(g), a, v, file)
	switch {
	case l.Offline:
		return "", fmt.Errorf("%w: %s (offline mode)", ErrOffline, rel)
	case snapshot:
		return l.fetchSnapshot(g, a, v, file, dest, verify)
	}
	for _, b := range l.bases() {
		err := l.download(b, rel, dest, verify)
		if err == nil {
			return dest, nil
		}
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return "", err
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, rel)
}

// FetchJar downloads an extra jar (used for the JUnit launcher).
func (l *Loader) FetchJar(g, a, v string) (string, error) {
	return l.fetchFile(g, a, v, a+"-"+v+".jar", true)
}

func (l *Loader) download(b repoRef, rel, dest string, verify bool) error {
	url := strings.TrimRight(b.URL, "/") + "/" + rel
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 400 * time.Millisecond)
		}
		if l.ctx.Err() != nil {
			return l.ctx.Err()
		}
		err = l.tryDownload(b, url, dest, verify)
		if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, errPermanent) {
			return err
		}
	}
	return err
}

func (l *Loader) get(ctx context.Context, b repoRef, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jv")
	if u, p := l.Settings.Auth(b.ID); u != "" && b.Trusted {
		if config.IsEncrypted(p) {
			decrypted, derr := config.DecryptPassword(p)
			if derr != nil {
				if _, seen := l.warned.LoadOrStore("decryptWarn:"+b.ID, true); !seen {
					l.warnf("could not decrypt password for server %q: %v — sending no credentials", b.ID, derr)
				}
			} else {
				req.SetBasicAuth(u, decrypted)
			}
		} else {
			req.SetBasicAuth(u, p)
		}
	}
	return l.client.Do(req)
}

func (l *Loader) tryDownload(b repoRef, url, dest string, verify bool) error {
	l.sem <- struct{}{}
	defer func() { <-l.sem }()
	if l.Verbose {
		fmt.Fprintf(os.Stderr, "[jv] GET %s\n", url)
	}

	ctx, cancel := context.WithCancel(l.ctx)
	defer cancel()
	idle := time.AfterFunc(stallTimeout, cancel)
	defer idle.Stop()

	resp, err := l.get(ctx, b, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, url)
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	default:
		return fmt.Errorf("%w: HTTP %d: %s", errPermanent, resp.StatusCode, url)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("%w: %v", errPermanent, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".jv-*")
	if err != nil {
		return fmt.Errorf("%w: %v", errPermanent, err)
	}
	h := sha1.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), &idleReader{r: resp.Body, t: idle, d: stallTimeout})
	idle.Stop()
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if verify {
		want, ok := l.fetchSum(b, url+".sha1")
		switch {
		case !ok:
			if os.Getenv("JV_REQUIRE_CHECKSUMS") != "" {
				os.Remove(tmp.Name())
				return fmt.Errorf("%w: no checksum available for %s (JV_REQUIRE_CHECKSUMS is set)", errPermanent, url)
			}
			if l.Verbose {
				fmt.Fprintf(os.Stderr, "[jv] warning: no checksum available for %s; not verified\n", url)
			}
		case want != hex.EncodeToString(h.Sum(nil)):
			os.Remove(tmp.Name())
			return fmt.Errorf("%w: checksum mismatch for %s", errPermanent, url)
		}
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("%w: %v", errPermanent, err)
	}
	return nil
}

func (l *Loader) fetchSum(b repoRef, url string) (string, bool) {
	ctx, cancel := context.WithTimeout(l.ctx, 30*time.Second)
	defer cancel()
	resp, err := l.get(ctx, b, url)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	tokens := strings.Fields(strings.ToLower(string(data)))
	for _, tok := range tokens {
		tok = strings.Trim(tok, "(),\"'")
		if len(tok) >= 40 {
			candidate := tok[:40]
			if _, err := hex.DecodeString(candidate); err == nil {
				return candidate, true
			}
		}
	}
	return "", false
}

func (l *Loader) module(g, a, v string) *Module {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mods[g+":"+a+":"+v]
}

// Register makes reactor modules resolvable as local artifacts.
func (l *Loader) Register(mods []*Module) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range mods {
		l.mods[m.GAV()] = m
	}
}

func (l *Loader) resetCaches() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.raws, l.effs = map[string]*rawEntry{}, map[string]*effEntry{}
}
