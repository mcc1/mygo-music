package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/lyrics"
	"mygo-music/internal/music"
	"mygo-music/internal/player"
)

// Choosing a style from the stage's menu sets its lyrics in it, and
// choosing Flow goes back to the classic.
func TestStageStyleMenu(t *testing.T) {
	a, tt, _ := playingApp()
	a.nowPlaying, a.npPane = true, paneStage
	a.pl.Seek(2 * time.Second)
	tt.Move(600, 400)       // the pointer in the window shows the bar the menu is in
	tt.Advance(time.Second) // Now Playing slides into place
	for _, c := range []struct{ item, want string }{{"Steps", "partita"}, {"Tilt", "tilt"}, {"Flow", ""}} {
		if err := tt.Click("Stage Style"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(c.item); err != nil {
			t.Fatalf("%v; the menu shows %q", err, tt.Menu())
		}
		tt.Frame()
		if a.settings.StageStyle != c.want {
			t.Errorf("chose %s: style %q, want %q", c.item, a.settings.StageStyle, c.want)
		}
	}
}

// Sparks in the stage's menu turns the sparks on, and again off, whatever
// the style.
func TestStageSparksMenu(t *testing.T) {
	a, tt, _ := playingApp()
	a.nowPlaying, a.npPane = true, paneStage
	a.settings.StageStyle = "tilt"
	tt.Move(600, 400)       // the pointer in the window shows the bar the menu is in
	tt.Advance(time.Second) // Now Playing slides into place
	for _, want := range []bool{true, false} {
		if err := tt.Click("Stage Style"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("Sparks"); err != nil {
			t.Fatalf("%v; the menu shows %q", err, tt.Menu())
		}
		tt.Frame()
		if a.settings.StageSparks != want || a.settings.StageStyle != "tilt" {
			t.Errorf("sparks %v in %q, want %v in tilt", a.settings.StageSparks, a.settings.StageStyle, want)
		}
	}
}

// Timed lines a source answers, each word with its time, reach the stage: the
// words being sung there, the line's translation and the line next under them.
func TestTimedLyricsReachTheStage(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	a.pl = player.New(a.post)
	a.pl.Source = a.sourceByID
	fake(a).lyrics = func(string) (*music.Lyrics, error) {
		return &music.Lyrics{Lines: []music.LyricLine{
			{Start: 1000, End: 3000, Translation: "你好世界", Words: []music.LyricWord{{Start: 1000, End: 2000, Text: "hello "}, {Start: 2000, End: 3000, Text: "world"}}},
			{Start: 4000, End: 6000, Text: "the next line"},
		}}, nil
	}
	tr := song("7", "宝船")
	tr.Source = "fake"
	a.pl.PlayQueue([]music.Track{tr}, 0)
	until(t, ch, func() bool { _, loaded := a.pl.Lyrics(); return loaded })
	if lines, _ := a.pl.Lyrics(); len(lines) != 2 || lines[0].Text != "hello world" {
		t.Fatalf("lines %+v", lines)
	}

	a.pl.Hold(2500 * time.Millisecond)
	a.nowPlaying, a.npPane = true, paneStage
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	settled(tt)
	for _, want := range []string{"hello ", "world", "你好世界", "the next line"} {
		if !tt.HasText(want) {
			t.Errorf("no %q on the stage: %q", want, tt.Texts())
		}
	}
}

// Untimed text reaches both lyric views without becoming a timed line, and
// switching tracks clears it before the next result comes back.
func TestPlainLyricsReachTheLyricViews(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	a.pl = player.New(a.post)
	a.pl.Source = a.sourceByID
	const plain = "First line\nSecond line\n\nNew verse"
	fake(a).lyrics = func(id string) (*music.Lyrics, error) {
		if id == "7" {
			return &music.Lyrics{Plain: plain}, nil
		}
		return &music.Lyrics{Plain: plain, LRC: "[00:01.00]Timed replacement"}, nil
	}
	tracks := []music.Track{song("7", "Untimed song"), song("8", "Timed song")}
	for i := range tracks {
		tracks[i].Source = "fake"
	}
	a.pl.PlayTrack(tracks, 0)
	until(t, ch, func() bool { _, loaded := a.pl.Lyrics(); return loaded })
	if lines, _ := a.pl.Lyrics(); len(lines) != 0 || a.pl.PlainLyrics() != plain {
		t.Fatalf("plain fallback became timed or changed its line breaks: %v", lines)
	}
	a.nowPlaying, a.npPane = true, paneLyrics
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	for _, pane := range []int{paneLyrics, paneStage} {
		a.npPane = pane
		settled(tt)
		if !tt.HasText(plain) {
			t.Fatalf("plain lyrics not visible in pane %d", pane)
		}
	}
	a.pl.Jump(1)
	if a.pl.PlainLyrics() != "" {
		t.Fatal("plain lyrics remained after changing tracks")
	}
	until(t, ch, func() bool { _, loaded := a.pl.Lyrics(); return loaded })
	if lines, _ := a.pl.Lyrics(); len(lines) != 1 || a.pl.PlainLyrics() != "" {
		t.Fatal("timed lyrics did not take priority")
	}
}

func words(texts ...string) []lyrics.Word {
	out := make([]lyrics.Word, len(texts))
	for i, s := range texts {
		out[i] = lyrics.Word{Text: s, Start: time.Duration(i) * time.Second, End: time.Duration(i+1) * time.Second}
	}
	return out
}

// A line breaks at its spaces and punctuation, punctuation staying with
// the word before it and a contraction whole, and a long run of
// characters in pieces.
func TestStageUnits(t *testing.T) {
	for _, c := range []struct {
		words []lyrics.Word
		want  [][2]int
	}{
		{words("从", "不 ", "主", "动 ", "示", "弱"), [][2]int{{0, 2}, {2, 4}, {4, 6}}},
		{words("Hello", ", ", "It", "’", "s ", "me"), [][2]int{{0, 2}, {2, 5}, {5, 6}}},
		{words("I ", "never ", "show"), [][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{words("曾", "经", "的", "那", "些", "发", "生", "过"), [][2]int{{0, 4}, {4, 8}}},
	} {
		got := stageUnits(c.words)
		if len(got) != len(c.want) {
			t.Errorf("%v: units %v, want %v", c.words, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%v: units %v, want %v", c.words, got, c.want)
				break
			}
		}
	}
}

// Tilt splits a line into parts that follow one another, none empty, and
// rather after punctuation than nearest the middle.
func TestTiltSplitsAtPunctuation(t *testing.T) {
	ws := words("夜", "空", "中", "最", "亮", "的", "星，", "能", "否", "听", "清")
	units := stageUnits(ws)
	for n := 1; n <= min(4, len(units)); n++ {
		parts := splitParts(ws, units, n)
		if len(parts) != n || parts[0].from != 0 || parts[n-1].to != len(ws) {
			t.Fatalf("%d parts: %+v", n, parts)
		}
		for i := 1; i < n; i++ {
			if parts[i].from != parts[i-1].to || parts[i].from >= parts[i].to {
				t.Fatalf("%d parts: %+v", n, parts)
			}
		}
	}
	if parts := splitParts(ws, units, 2); parts[0].to != 7 {
		t.Errorf("split at %d, not after 星，", parts[0].to)
	}
}
