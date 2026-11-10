package player

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"slices"
	"testing"

	"github.com/tphakala/go-opus/oggopus"
)

// An Ogg Opus download plays before completion and seeks without exposing
// codec delay or padding as extra samples.
func TestOggOpusPlaybackAndSeek(t *testing.T) {
	const rate = oggopus.OutputSampleRate
	const frames = 2*rate + 123
	pcm := make([]byte, frames*2)
	for i := range frames {
		sample := int16(16000 * math.Sin(2*math.Pi*997*float64(i)/rate))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}
	var encoded bytes.Buffer
	if err := oggopus.EncodeInterleaved(&encoded, oggopus.Config{SampleRate: rate, Channels: 1}, pcm); err != nil {
		t.Fatal(err)
	}

	dl := downloaded(encoded.Bytes())
	dl.done = false
	playing, err := openStream(dl, 2000)
	if err != nil {
		t.Fatal(err)
	}
	defer playing.Close()
	if !SupportsAudio("ogg", "opus", "progressive") || SupportsAudio("webm", "opus", "progressive") {
		t.Fatal("Opus capability does not match the readable container")
	}
	if playing.SampleRate() != rate || playing.Channels() != 2 || playing.Len() != 2*rate {
		t.Fatalf("streaming Opus reported %d Hz, %d channels, %d frames", playing.SampleRate(), playing.Channels(), playing.Len())
	}
	buf := make([]float32, 2048)
	n, err := playing.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("progressive read returned %d samples, %v", n, err)
	}
	if err := playing.SetPosition(rate / 4); err != nil {
		t.Fatalf("seek while still downloading: %v", err)
	}
	dl.mu.Lock()
	dl.done = true
	dl.cond.Broadcast()
	dl.mu.Unlock()

	straight, err := openStream(downloaded(encoded.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer straight.Close()
	if got := straight.Len(); got != frames {
		t.Fatalf("Opus length = %d, want %d", got, frames)
	}
	var ref []float32
	for {
		n, err := straight.Read(buf)
		ref = append(ref, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil || n == 0 {
			t.Fatalf("sequential read returned %d samples, %v", n, err)
		}
	}
	if len(ref) != frames*2 {
		t.Fatalf("decoded %d samples, want %d", len(ref), frames*2)
	}
	for i := 0; i < len(ref); i += 2 {
		if ref[i] != ref[i+1] {
			t.Fatal("mono did not play in both channels")
		}
	}
	for _, frame := range []int64{rate / 2, frames - 31, 0} {
		if err := playing.SetPosition(frame); err != nil {
			t.Fatalf("seek to %d: %v", frame, err)
		}
		n, err := playing.Read(buf)
		if err != nil || n == 0 || !slices.Equal(buf[:n], ref[frame*2:frame*2+int64(n)]) {
			t.Fatalf("seek to %d did not land on the reference samples", frame)
		}
	}
}
