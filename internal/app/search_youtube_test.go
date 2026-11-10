package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// Only valid video IDs and recognized YouTube video links reach the lookup.
func TestYouTubeSearchID(t *testing.T) {
	const id = "83uv0HCUGD8"
	for _, input := range []string{
		id, "v=" + id, "?v=" + id + "&list=PLtest",
		"https://music.youtube.com/watch?v=" + id,
		"www.youtube.com/watch?v=" + id + "&t=42",
		"https://youtu.be/" + id + "?si=share",
		"https://m.youtube.com/shorts/" + id,
		"https://www.youtube.com/live/" + id,
		"https://www.youtube.com/embed/" + id,
	} {
		if got, explicit := youtubeSearchID(input); got != id || explicit != (input != id) {
			t.Errorf("%q returned %q, explicit %v", input, got, explicit)
		}
	}
	for _, input := range []string{
		"ordinary keywords", "v=wrong", "https://youtube.com/playlist?list=PL123",
		"https://youtube.com.example.net/watch?v=" + id,
		"https://somebody@youtube.com/watch?v=" + id,
		"https://youtu.be/" + id + "/extra",
	} {
		if got, _ := youtubeSearchID(input); got != "" {
			t.Errorf("%q was interpreted as %q", input, got)
		}
	}
}

type linkSearchSource struct {
	*fakeSource
	resolve func(string) *music.Track
}

func (s linkSearchSource) Can(what string) bool {
	return what == "youtubeTrack" || s.fakeSource.Can(what)
}

func (s linkSearchSource) YouTubeTrack(id string) (*music.Track, error) {
	return s.resolve(id), nil
}

// Enter on a YouTube link plays the exact result even from a different source.
func TestYouTubeSearchEnterPlaysTrack(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	ordinary := fakeSearch(a)
	const id = "83uv0HCUGD8"
	addFake(a, linkSearchSource{newFakeSource("youtube"), func(got string) *music.Track {
		return &music.Track{ID: got, Source: "youtube", Name: "Exact song"}
	}})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	tt.Type("https://youtu.be/" + id)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	until(t, ch, func() bool { return a.pl.Current() != nil })
	got := a.pl.Current()
	if got.ID != id || got.Source != "youtube" || got.Name != "Exact song" || !a.pl.Playing() || len(ordinary) != 0 {
		t.Fatalf("current track %+v, playing %v, ordinary searches %d", got, a.pl.Playing(), len(ordinary))
	}
}

// An eleven-character keyword is still searched when it isn't a video ID.
func TestYouTubeSearchBareKeywordFallsBack(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	ordinary := fakeSearch(a)
	addFake(a, linkSearchSource{newFakeSource("youtube"), func(string) *music.Track { return nil }})
	a.search("imagination")
	until(t, ch, func() bool { return a.route.page == pageSearch })
	if got := wait(t, ordinary, "keyword search"); got.query != "imagination" {
		t.Fatalf("searched %q", got.query)
	}
}
