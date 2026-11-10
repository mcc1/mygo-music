package player

import (
	"io"
	"os"
	"testing"
)

// The WebM reader removes encoder padding and uses the cues to seek to a
// different part of a stream whose opening bytes are already available.
func TestWebMOpusPlaybackAndSeek(t *testing.T) {
	data, err := os.ReadFile("testdata/seek.webm")
	if err != nil {
		t.Fatal(err)
	}
	dl := downloaded(data)
	dl.done = false
	s, err := openStream(dl, 4000)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !SupportsAudio("webm", "opus", "progressive") || SupportsAudio("webm", "vorbis", "progressive") {
		t.Fatal("wrong WebM playback capabilities")
	}
	buf := make([]float32, 4800*2)
	crossings := func() int {
		t.Helper()
		n, err := s.Read(buf)
		if n != len(buf) || err != nil {
			t.Fatalf("read %d samples: %v", n, err)
		}
		count := 0
		for i := 2; i < n; i += 2 {
			if buf[i-2] <= 0 && buf[i] > 0 {
				count++
			}
		}
		return count
	}
	if got := crossings(); got < 20 || got > 24 {
		t.Fatalf("opening 220 Hz tone has %d crossings", got)
	}
	if err := s.SetPosition(5 * webmRate / 2); err != nil {
		t.Fatal(err)
	}
	d := s.(*decodedStream).Decoder.(*webmOpus)
	if len(d.cues) < 2 {
		t.Fatal("did not read the WebM cue table")
	}
	if got := crossings(); got < 42 || got > 46 {
		t.Fatalf("after seeking, the 440 Hz tone has %d crossings", got)
	}
	if err := s.SetPosition(0); err != nil {
		t.Fatal(err)
	}
	samples := 0
	for {
		n, err := s.Read(buf)
		samples += n
		if err == io.EOF {
			break
		}
		if err != nil || n == 0 {
			t.Fatalf("read %d samples: %v", n, err)
		}
	}
	if samples != 4*webmRate*2 {
		t.Fatalf("decoded %d samples, want %d", samples, 4*webmRate*2)
	}
}
