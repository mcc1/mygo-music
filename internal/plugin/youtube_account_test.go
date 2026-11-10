package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

const testLegacyYouTubeCookie = "SAPISID=synthetic-sid; SID=synthetic-session"

type testYouTubeStore struct {
	data        []byte
	badReadback bool
	deleteError bool
}

func (s *testYouTubeStore) Read() ([]byte, error) {
	if s.badReadback && len(s.data) != 0 {
		return nil, errors.New("credential unavailable after write")
	}
	return bytes.Clone(s.data), nil
}

func (s *testYouTubeStore) Write(data []byte) error {
	s.data = bytes.Clone(data)
	return nil
}

func (s *testYouTubeStore) Delete() error {
	if s.deleteError {
		return errors.New("credential store locked")
	}
	s.data = nil
	return nil
}

func testYouTubePlugin(dir string, store *testYouTubeStore, legacy bool) *Plugin {
	p := &Plugin{data: dir, manifest: Manifest{YouTubeAccount: true}, storage: map[string]json.RawMessage{"other": json.RawMessage(`"preserved"`)}}
	p.youtube.store = store
	if legacy {
		p.legacyYouTubeCookie, _ = json.Marshal(testLegacyYouTubeCookie)
	}
	return p
}

// Migration keeps the old cookie until its stored replacement can be read back.
func TestYouTubeMigrationRequiresReadback(t *testing.T) {
	dir := t.TempDir()
	store := &testYouTubeStore{badReadback: true}
	p := testYouTubePlugin(dir, store, true)
	if err := p.writeStorageLocked(); err != nil {
		t.Fatal(err)
	}
	p.loadYouTubeLocked()
	if p.youtube.session.Legacy != testLegacyYouTubeCookie || p.youtube.notice != "migration" {
		t.Fatal("failed migration lost the active account or its notice")
	}
	if err := p.writeStorageLocked(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "storage.json"))
	if err != nil || !bytes.Contains(data, []byte(testLegacyYouTubeCookie)) {
		t.Fatal("legacy session was removed before verification")
	}
	store.badReadback = false
	restarted := testYouTubePlugin(dir, store, true)
	restarted.loadYouTubeLocked()
	if restarted.youtube.session.Legacy != testLegacyYouTubeCookie || restarted.youtube.notice != "" {
		t.Fatal("restart did not complete migration using the verified session")
	}
	data, err = os.ReadFile(filepath.Join(dir, "storage.json"))
	if err != nil || bytes.Contains(data, []byte("synthetic-session")) || !bytes.Contains(data, []byte("preserved")) {
		t.Fatal("migration did not remove only the plaintext credential")
	}
	if err := restarted.signOutYouTube(); err != nil {
		t.Fatal(err)
	}
	if len(store.data) != 0 || restarted.youtube.session.Legacy != "" {
		t.Fatal("sign-out left a stored or active session")
	}
}

// Sign-out must stay signed out even if the platform store was locked.
func TestYouTubeSignOutDoesNotRestoreLockedSession(t *testing.T) {
	dir := t.TempDir()
	store := &testYouTubeStore{data: []byte(`{"legacy":"SAPISID=synthetic"}`), deleteError: true}
	p := testYouTubePlugin(dir, store, false)
	p.loadYouTubeLocked()
	if err := p.signOutYouTube(); err != nil {
		t.Fatal(err)
	}
	store.deleteError = false
	restarted := testYouTubePlugin(dir, store, false)
	restarted.loadYouTubeLocked()
	if len(store.data) != 0 || restarted.youtube.session.Legacy != "" {
		t.Fatal("the previously locked session was restored")
	}
}

type youtubeTestTransport func(*http.Request) (*http.Response, error)

func (t youtubeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return t(r) }

// Only the selected YouTube requests can receive the managed credential.
func TestYouTubeAccountTransportScope(t *testing.T) {
	p := testYouTubePlugin(t.TempDir(), &testYouTubeStore{}, false)
	p.youtube.loaded = true
	p.youtube.session = youtubeSession{Legacy: testLegacyYouTubeCookie}
	for _, tc := range []struct {
		method, url                           string
		marked, reachesNetwork, authenticated bool
	}{
		{"POST", "https://music.youtube.com/youtubei/v1/account/account_menu", true, true, true},
		{"POST", "https://music.youtube.com/youtubei/v1/player", false, true, false},
		{"GET", "https://s.youtube.com/api/stats/playback", true, true, false},
		{"POST", "https://music.youtube.com/youtubei/v1/visitor_id", true, false, false},
		{"GET", "https://api.betterlyrics.org/getLyrics", true, false, false},
		{"POST", "http://music.youtube.com/youtubei/v1/browse", true, false, false},
		{"POST", "https://music.youtube.com:443/youtubei/v1/browse", true, false, false},
		{"POST", "https://music.youtube.com/youtubei/v1/%62rowse", true, false, false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			r, err := http.NewRequest(tc.method, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.marked {
				r.Header.Set(youtubeMarker, "1")
			}
			called := false
			base := youtubeTestTransport(func(req *http.Request) (*http.Response, error) {
				called = true
				if req.Header.Get(youtubeMarker) != "" {
					t.Error("internal marker reached the network")
				}
				gotCookie, gotAuth := req.Header.Get("Cookie"), req.Header.Get("Authorization")
				if tc.authenticated {
					if gotCookie != testLegacyYouTubeCookie || !strings.HasPrefix(gotAuth, "SAPISIDHASH ") {
						t.Error("allowed request did not get its signed session")
					}
				} else if gotCookie != "" || gotAuth != "" {
					t.Error("an anonymous request received credentials")
				}
				return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
			})
			if _, err := (youtubeTransport{plugin: p, base: base}).RoundTrip(r); (err == nil) != tc.reachesNetwork || called != tc.reachesNetwork {
				t.Fatalf("transport called=%v, err=%v", called, err)
			}
		})
	}
}

// A redirect from an authenticated request cannot make a second request.
func TestYouTubeAccountRedirectStops(t *testing.T) {
	p := testYouTubePlugin(t.TempDir(), &testYouTubeStore{}, false)
	p.youtube.loaded, p.youtube.session.Legacy = true, testLegacyYouTubeCookie
	calls := 0
	c := &http.Client{
		CheckRedirect: stopYouTubeAccountRedirect,
		Transport: youtubeTransport{plugin: p, base: youtubeTestTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"https://music.youtube.com/youtubei/v1/search"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})},
	}
	r, _ := http.NewRequest("POST", "https://music.youtube.com/youtubei/v1/browse", nil)
	r.Header.Set(youtubeMarker, "1")
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls != 1 || resp.StatusCode != 307 {
		t.Fatalf("followed a credentialed redirect, calls=%d, status=%d", calls, resp.StatusCode)
	}
}

// New sessions retain the WebView's cookie scope instead of replaying a header.
func TestYouTubeAccountCookieScope(t *testing.T) {
	now := time.Now()
	session := youtubeSession{Cookies: []mygo.Cookie{
		{Name: "SAPISID", Value: "sid", Domain: ".youtube.com", Path: "/", Secure: true},
		{Name: "MUSIC", Value: "music", Domain: "music.youtube.com", Path: "/youtubei", Secure: true},
		{Name: "EXPIRED", Value: "expired", Domain: ".youtube.com", Path: "/", Expires: now.Add(-time.Hour)},
		{Name: "PATH", Value: "path", Domain: ".youtube.com", Path: "/api/stats/other"},
	}}
	for _, tc := range []struct{ host, path, want string }{
		{"music.youtube.com", "/youtubei/v1/browse", "MUSIC=music; SAPISID=sid"},
		{"s.youtube.com", "/api/stats/playback", "SAPISID=sid"},
	} {
		r, _ := http.NewRequest("GET", fmt.Sprintf("https://%s%s", tc.host, tc.path), nil)
		if cookie, sid := session.credentials(r.URL, now); cookie != tc.want || sid != "sid" {
			t.Fatalf("incorrect cookie selection for %s", tc.host)
		}
	}
}

// The plugin VM gets account state and responses without receiving raw cookies.
func TestYouTubeAccountHostKeepsCredentialsInGo(t *testing.T) {
	oldSignIn := youtubeWebSignIn
	t.Cleanup(func() { youtubeWebSignIn = oldSignIn })
	SetYouTubeWebSignIn(func(WebSignIn) ([]mygo.Cookie, error) {
		return []mygo.Cookie{
			{Name: "SAPISID", Value: "synthetic", Domain: ".youtube.com", Path: "/", Secure: true},
		}, nil
	})
	p := load(t, `{"id":"managed","youtubeAccount":true,"permissions":{"network":["music.youtube.com"]}}`, `
export async function signIn() {
  const result = await host.webSignIn({
    url: "https://accounts.google.com/ServiceLogin",
    cookiesOf: "https://music.youtube.com",
    until: ["SAPISID"],
  });
  if (result !== undefined || host.storage.get("cookie") !== undefined) throw new Error("credential reached the runtime");
  if (!host.youtubeAccount.state().active) throw new Error("session is inactive");
  return { id: "synthetic-account", name: "Account" };
}
export function signOut() { host.youtubeAccount.signOut(); }
`)
	store := &testYouTubeStore{}
	p.youtube.store = store
	acc, err := p.SignIn()
	if err != nil || acc == nil || acc.ID != "synthetic-account" || len(store.data) == 0 {
		t.Fatalf("secure sign-in failed: %v", err)
	}
	if err := p.SignOut(); err != nil || len(store.data) != 0 {
		t.Fatalf("secure sign-out failed: %v", err)
	}
}
