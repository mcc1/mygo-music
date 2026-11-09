package player

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"mygo-music/internal/music"
)

type rangeTransport func(*http.Request) (*http.Response, error)

func (f rangeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadFileReadsUnbufferedRange(t *testing.T) {
	const size, offset = 6 * rangeBlockSize, 4*rangeBlockSize + 32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &download{size: size, n: 1024, data: make([]byte, 1024)}
	d.cond.L = &d.mu
	d.ranges = newAudioRanges(ctx, &music.Stream{URL: "https://audio.example/track", Size: size})
	f := &downloadFile{dl: d}
	block := bytes.Repeat([]byte{0x42}, rangeBlockSize)
	var calls int
	d.ranges.client = &http.Client{Transport: rangeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		start, end := 4*rangeBlockSize, 5*rangeBlockSize-1
		if got, want := r.Header.Get("Range"), fmt.Sprintf("bytes=%d-%d", start, end); got != want {
			t.Fatalf("Range %q, want %q", got, want)
		}
		return &http.Response{
			StatusCode: http.StatusPartialContent, ContentLength: int64(len(block)),
			Header: http.Header{"Content-Range": {fmt.Sprintf("bytes %d-%d/%d", start, end, size)}},
			Body:   io.NopCloser(bytes.NewReader(block)),
		}, nil
	})}
	got := make([]byte, 64)
	for _, at := range []int64{offset, offset + 64} {
		if n, err := f.ReadAt(got, at); err != nil || n != len(got) || !bytes.Equal(got, block[:len(got)]) {
			t.Fatalf("read at %d: %d bytes, %v", at, n, err)
		}
	}
	if calls != 1 || d.n != 1024 {
		t.Fatalf("range calls=%d, sequential bytes=%d", calls, d.n)
	}
}

func TestDownloadFileFallsBackWhenRangeIsIgnored(t *testing.T) {
	const size, offset = 3 * rangeBlockSize, 2 * rangeBlockSize
	d := &download{size: size}
	d.cond.L = &d.mu
	d.ranges = newAudioRanges(context.Background(), &music.Stream{URL: "https://audio.example/track", Size: size})
	f := &downloadFile{dl: d}
	data := make([]byte, size)
	data[offset] = 7
	calls := 0
	d.ranges.client = &http.Client{Transport: rangeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		d.write(data)
		return &http.Response{
			StatusCode: http.StatusOK, ContentLength: int64(len(data)),
			Body: io.NopCloser(bytes.NewReader(data)),
		}, nil
	})}
	b := make([]byte, 1)
	if n, err := f.ReadAt(b, offset); err != nil || n != 1 || b[0] != 7 {
		t.Fatalf("sequential fallback: %v, %d bytes, %v", b, n, err)
	}
	if !d.ranges.disabled || calls != 1 {
		t.Fatalf("range attempts=%d, disabled=%v", calls, d.ranges.disabled)
	}
}
