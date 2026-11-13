package app

import (
	"testing"
	"time"

	"mygo-music/internal/i18n"
	"mygo-music/internal/lyrics"
)

func TestPodcastCaptionDisplay(t *testing.T) {
	var display captionDisplay
	source := []lyrics.Line{
		{At: time.Second, End: 2 * time.Second, Text: "这个节目欢迎大家"},
		{Text: "日本語の番組"},
	}
	got := display.lines("episode", source, i18n.TraditionalChinese)
	if got[0].Text != "這個節目歡迎大家" || got[1].Text != source[1].Text {
		t.Fatalf("unexpected captions: %+v", got)
	}
	if got[0].At != source[0].At || got[0].End != source[0].End {
		t.Fatalf("changed timestamps: %+v", got[0])
	}
	got = display.lines("episode", source, i18n.Chinese)
	if got[0].Text != "这个节目欢迎大家" {
		t.Fatalf("original transcript changed: %+v", got[0])
	}
}
