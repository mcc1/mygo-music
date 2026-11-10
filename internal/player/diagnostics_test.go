package player

import (
	"testing"

	"mygo-music/internal/music"
)

// Diagnostics follow the current transfer path and report all received ranges.
func TestDiagnosticsTrackRangeFallback(t *testing.T) {
	dl := &download{n: 128, size: 1024, ranges: &audioRanges{received: 512}}
	p := &Player{stream: &mp3Stream{dl: dl, rate: 44100}}
	if got, ok := p.Diagnostics(); !ok || got.Transfer != "range" || got.Received != 512 || got.Total != 1024 {
		t.Fatalf("range diagnostics: %+v", got)
	}
	dl.ranges.fallback = true
	if got, ok := p.Diagnostics(); !ok || got.Transfer != "range-fallback" || got.Received != 128 {
		t.Fatalf("fallback diagnostics: %+v", got)
	}
	dl.cached, dl.done = true, true
	if got, ok := p.Diagnostics(); !ok || got.Transfer != "cache" || !got.Complete {
		t.Fatalf("cached diagnostics: %+v", got)
	}
}

func TestDiagnosticsTrackBitrate(t *testing.T) {
	p := &Player{
		stream: &mp3Stream{dl: &download{size: 24000}, rate: 44100},
		track:  &music.Track{Duration: 1000},
		info:   &music.Stream{BitRate: 128000},
	}
	if got, _ := p.Diagnostics(); got.BitRate != 128000 || got.EstimatedBitRate ||
		got.Container != "mp3" || got.Codec != "mp3" || got.SampleRate != 44100 || got.Channels != 2 {
		t.Fatalf("reported stream metadata: %+v", got)
	}
	p.info.BitRate = 0
	if got, _ := p.Diagnostics(); got.BitRate != 192000 || !got.EstimatedBitRate {
		t.Fatalf("estimated bitrate: %+v", got)
	}
}
