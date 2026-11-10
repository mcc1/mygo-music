package app

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
	"mygo-music/internal/player"
)

// The setting reveals playback diagnostics without restarting the player.
func TestPlaybackDiagnosticsSwitch(t *testing.T) {
	a := newTestApp()
	a.settingsPage = "playback"
	a.pl.Cue([]music.Track{song("7", "Test song")})
	playerView := ui.NewTester(a.view, 1000, 760)
	playerView.Frame()
	waiting := T("Waiting for stream details")
	if slices.Contains(playerView.Texts(), waiting) {
		t.Fatal("diagnostics visible by default")
	}
	settingsView := ui.NewTester(a.settingsView, 760, 1000)
	settingsView.Frame()
	title, foundTitle := settingsView.Find(T("Playback Diagnostics"))
	desc, foundDesc := settingsView.Find(T("Show the audio format, playback route and download status in the player"))
	if !foundTitle || !foundDesc {
		t.Fatal("could not find the playback diagnostics switch")
	}
	settingsView.ClickAt(760-28-16-12, (title.Y+desc.Y+desc.H)/2)
	settingsView.Frame()
	playerView.Frame()
	if !a.settings.PlaybackDiagnostics || !slices.Contains(playerView.Texts(), waiting) {
		t.Fatal("switch did not reveal diagnostics")
	}
	label, found := playerView.Find(waiting)
	if !found || label.X < 780 || label.Y+label.H >= 760-playerBarHeight {
		t.Fatalf("diagnostics should float at the right, above the player bar: %+v", label)
	}
}

// A source that offers Opus preference makes its playback switch usable.
func TestPreferOpusSettingWithCapableSource(t *testing.T) {
	a := newTestApp()
	src := newFakeSource("opus")
	src.caps["streamWithOpus"] = true
	addFake(a, src)
	a.settingsPage = "playback"
	view := ui.NewTester(a.settingsView, 760, 1000)
	view.Frame()
	title, foundTitle := view.Find(T("Prefer Opus"))
	desc, foundDesc := view.Find(T("Use Opus when available in YouTube Music, starting with the next track you open"))
	if !foundTitle || !foundDesc {
		t.Fatal("could not find the Opus switch for a capable source")
	}
	view.ClickAt(760-28-16-12, (title.Y+desc.Y+desc.H)/2)
	view.Frame()
	if !a.settings.PreferOpus {
		t.Fatal("switch did not enable Opus preference")
	}
}

// Only recognized format and route values are rendered, even for plugin input.
func TestPlaybackDetailsDoNotRenderRawPluginData(t *testing.T) {
	d := player.PlaybackDiagnostics{
		Container: "https://invalid.example/token", Codec: "private-header",
		Processing: "private-session", Resolver: "private-resolver", Transfer: "cache",
	}
	if got := playbackDetails(d); !slices.Equal(got, []string{T("Cache hit")}) {
		t.Fatalf("unexpected diagnostic text %q", got)
	}
}
