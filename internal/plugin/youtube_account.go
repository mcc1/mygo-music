package plugin

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/egoist/mygo"

	"mygo-music/internal/secretstore"
)

const (
	youtubeOrigin = "https://music.youtube.com"
	youtubeMarker = "X-MyGo-YouTube-Account"
	youtubeLogout = "youtube-session-signed-out.json"
)

type youtubeSession struct {
	Legacy  string        `json:"legacy,omitempty"`
	Cookies []mygo.Cookie `json:"cookies,omitempty"`
}

type youtubeAccount struct {
	mu      sync.Mutex
	store   secretstore.Store
	loaded  bool
	session youtubeSession
	notice  string
}

var youtubeWebSignIn = func(WebSignIn) ([]mygo.Cookie, error) {
	return nil, errors.New("can't show a sign-in page here")
}

func SetYouTubeWebSignIn(show func(WebSignIn) ([]mygo.Cookie, error)) { youtubeWebSignIn = show }

func validYouTubeSignIn(o WebSignIn) bool {
	u, err := url.Parse(o.URL)
	return err == nil && u.Scheme == "https" && u.Host == "accounts.google.com" && u.User == nil &&
		u.Path == "/ServiceLogin" && o.CookiesOf == youtubeOrigin
}

// Ordinary plugin storage writes keep a pending legacy migration on disk.
// The credential itself is never in the map that the runtime can read.
func (p *Plugin) writeStorageLocked() error {
	storage := p.storage
	if len(p.legacyYouTubeCookie) > 0 {
		storage = maps.Clone(storage)
		storage["cookie"] = p.legacyYouTubeCookie
	}
	return p.writeJSON("storage.json", storage)
}

func (p *Plugin) removeLegacyYouTubeCookie() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.legacyYouTubeCookie) == 0 {
		return nil
	}
	if err := p.writeJSON("storage.json", p.storage); err != nil {
		return err
	}
	p.legacyYouTubeCookie = nil
	return nil
}

func (p *Plugin) loadYouTubeLocked() {
	a := &p.youtube
	if a.loaded {
		return
	}
	a.loaded = true
	var signedOut bool
	readJSON(filepath.Join(p.data, youtubeLogout), &signedOut)
	if signedOut {
		// Retry deleting a credential the last logout couldn't reach.
		_ = a.store.Delete()
		return
	}
	p.mu.Lock()
	raw := p.legacyYouTubeCookie
	p.mu.Unlock()
	var legacy string
	_ = json.Unmarshal(raw, &legacy)
	if strings.ContainsAny(legacy, "\r\n\x00") {
		legacy = ""
	}
	saved, err := a.store.Read()
	var session youtubeSession
	if err == nil && len(saved) > 0 && json.Unmarshal(saved, &session) == nil {
		a.session = session
		if legacy != "" && (a.session.Legacy != legacy || p.removeLegacyYouTubeCookie() != nil) {
			a.notice = "migration"
		}
		return
	}
	if legacy == "" {
		return
	}
	a.session = youtubeSession{Legacy: legacy}
	if err != nil || a.saveVerified(a.session) != nil || p.removeLegacyYouTubeCookie() != nil {
		a.notice = "migration"
	}
}

func (a *youtubeAccount) saveVerified(session youtubeSession) error {
	encoded, err := json.Marshal(session)
	if err != nil {
		return err
	}
	if err := a.store.Write(encoded); err != nil {
		return err
	}
	saved, err := a.store.Read()
	if err != nil || !bytes.Equal(saved, encoded) {
		return errors.New("could not verify the stored YouTube session")
	}
	return nil
}

func (p *Plugin) signInYouTube(o WebSignIn) error {
	cookies, err := youtubeWebSignIn(o)
	if err != nil {
		return err
	}
	session := youtubeSession{Cookies: cookies}
	if _, sid := session.credentials(&url.URL{Scheme: "https", Host: "music.youtube.com", Path: "/youtubei/v1/account/account_menu"}, time.Now()); sid == "" {
		return errors.New("YouTube did not provide a usable account session")
	}
	a := &p.youtube
	a.mu.Lock()
	defer a.mu.Unlock()
	p.loadYouTubeLocked()
	a.session = session
	if a.saveVerified(session) != nil {
		a.notice = "memory"
		return nil
	}
	// A durable session supersedes an earlier local sign-out. Store the
	// tombstone only when needed, and remove it after readback succeeds.
	if err := os.Remove(filepath.Join(p.data, youtubeLogout)); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.notice = "memory"
		return nil
	}
	a.notice = ""
	if err := p.removeLegacyYouTubeCookie(); err != nil {
		a.notice = "migration"
	}
	return nil
}

func (p *Plugin) signOutYouTube() error {
	a := &p.youtube
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := p.removeLegacyYouTubeCookie(); err != nil {
		return err
	}
	if err := a.store.Delete(); err != nil {
		// An unavailable keyring must not restore an older session after
		// sign-out. The marker contains no credentials.
		if err := p.writeJSON(youtubeLogout, true); err != nil {
			return err
		}
	}
	a.loaded, a.session, a.notice = true, youtubeSession{}, ""
	return nil
}

func (p *Plugin) youtubeAccountAPI(v *vm) map[string]any {
	state := func(_ *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
		a := &p.youtube
		a.mu.Lock()
		defer a.mu.Unlock()
		p.loadYouTubeLocked()
		now := time.Now()
		_, sid := a.session.credentials(&url.URL{Scheme: "https", Host: "music.youtube.com", Path: "/youtubei/v1/account/account_menu"}, now)
		_, history := a.session.credentials(&url.URL{Scheme: "https", Host: "s.youtube.com", Path: "/api/stats/playback"}, now)
		return v.rt.FromGo(map[string]any{"active": sid != "", "history": history != "", "notice": a.notice})
	}
	return map[string]any{
		"state":              moejs.NativeFunc(state),
		"signatureTimestamp": moejs.NativeFunc(p.youtubeSignatureTimestampAPI(v)),
		"decipherAudio":      moejs.NativeFunc(p.youtubeDecipherAudioAPI(v)),
		"signOut": moejs.NativeFunc(func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.Undefined(), p.signOutYouTube()
		}),
	}
}

// credentials selects cookies using the WebView's domain, path and expiry.
// A migrated header is pinned to its original origin.
func (s youtubeSession) credentials(u *url.URL, now time.Time) (string, string) {
	if s.Legacy != "" {
		if u.Host != "music.youtube.com" {
			return "", ""
		}
		r := http.Request{Header: http.Header{"Cookie": {s.Legacy}}}
		return s.Legacy, signingCookie(r.Cookies())
	}
	var matching []mygo.Cookie
	for _, c := range s.Cookies {
		domain := strings.ToLower(c.Domain)
		host := u.Hostname()
		if domain != host && !(strings.HasPrefix(domain, ".") && (host == domain[1:] || strings.HasSuffix(host, domain))) {
			continue
		}
		if (!c.Expires.IsZero() && !c.Expires.After(now)) || (c.Secure && u.Scheme != "https") {
			continue
		}
		path := c.Path
		if path == "" {
			path = "/"
		}
		if u.Path != path && !strings.HasPrefix(u.Path, strings.TrimSuffix(path, "/")+"/") {
			continue
		}
		matching = append(matching, c)
	}
	sort.SliceStable(matching, func(i, j int) bool { return len(matching[i].Path) > len(matching[j].Path) })
	r := http.Request{Header: make(http.Header)}
	var selected []*http.Cookie
	for _, c := range matching {
		cookie := &http.Cookie{Name: c.Name, Value: c.Value}
		if c.Value != "" && cookie.Valid() == nil {
			r.AddCookie(cookie)
			selected = append(selected, cookie)
		}
	}
	return r.Header.Get("Cookie"), signingCookie(selected)
}

func signingCookie(cookies []*http.Cookie) string {
	var sid string
	for _, c := range cookies {
		switch c.Name {
		case "__Secure-3PAPISID":
			if c.Value != "" {
				return c.Value
			}
		case "SAPISID":
			if sid == "" {
				sid = c.Value
			}
		}
	}
	return sid
}

type youtubeTransport struct {
	plugin *Plugin
	base   http.RoundTripper
}

func (t youtubeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if r.Header.Get(youtubeMarker) == "" {
		return base.RoundTrip(r)
	}
	if !validYouTubeAccountRequest(r) {
		return nil, errors.New("YouTube account request is outside its allowed endpoints")
	}
	req := r.Clone(r.Context())
	req.Header.Del(youtubeMarker)
	req.Header.Del("Cookie")
	req.Header.Del("Authorization")
	a := &t.plugin.youtube
	a.mu.Lock()
	t.plugin.loadYouTubeLocked()
	now := time.Now()
	cookie, sid := a.session.credentials(req.URL, now)
	a.mu.Unlock()
	if sid != "" {
		origin := "https://" + req.URL.Host
		hash := sha1.Sum([]byte(fmt.Sprintf("%d %s %s", now.Unix(), sid, origin)))
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Authorization", fmt.Sprintf("SAPISIDHASH %d_%x", now.Unix(), hash))
		req.Header.Set("X-Goog-Authuser", "0")
	}
	return base.RoundTrip(req)
}

func validYouTubeAccountRequest(r *http.Request) bool {
	u := r.URL
	if u.Scheme != "https" || u.User != nil || u.RawPath != "" || (r.Host != "" && r.Host != u.Host) {
		return false
	}
	if u.Host == "s.youtube.com" {
		return r.Method == http.MethodGet && u.Path == "/api/stats/playback"
	}
	if u.Host != "music.youtube.com" || r.Method != http.MethodPost {
		return false
	}
	switch u.Path {
	case "/youtubei/v1/account/account_menu", "/youtubei/v1/browse", "/youtubei/v1/search",
		"/youtubei/v1/music/get_queue", "/youtubei/v1/music/get_search_suggestions",
		"/youtubei/v1/like/like", "/youtubei/v1/like/removelike", "/youtubei/v1/player", "/youtubei/v1/next":
		return true
	}
	return false
}

func stopYouTubeAccountRedirect(_ *http.Request, via []*http.Request) error {
	if len(via) > 0 && via[0].Header.Get(youtubeMarker) != "" {
		return http.ErrUseLastResponse
	}
	return nil
}
