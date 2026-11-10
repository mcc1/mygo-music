package player

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"mygo-music/internal/music"
)

func rangeFixture() []byte {
	data := make([]byte, 3*rangeBlockSize+127)
	for i := range data {
		data[i] = byte(1 + i/rangeBlockSize)
	}
	return data
}

// A slow 1 MiB block is retried in 256 KiB pieces; if one fails, a single
// sequential response resumes from the previously verified prefix.
func TestRangeTimeoutRetriesOnlySlowBlock(t *testing.T) {
	for _, failRetry := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail-retry=%v", failRetry), func(t *testing.T) {
			t.Parallel()
			data := rangeFixture()
			var mu sync.Mutex
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				header := r.Header.Get("Range")
				mu.Lock()
				requests = append(requests, header)
				mu.Unlock()
				if header == fmt.Sprintf("bytes=%d-", rangeBlockSize) {
					sendRange(w, data, rangeBlockSize, len(data)-1)
					return
				}
				var start, end int
				if n, _ := fmt.Sscanf(header, "bytes=%d-%d", &start, &end); n != 2 {
					t.Error("unexpected request for the full stream")
					http.Error(w, "unexpected", http.StatusBadRequest)
					return
				}
				switch {
				case start == rangeBlockSize && end-start+1 == rangeBlockSize:
					// The headers arrive correctly, but the block itself stalls.
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
					w.WriteHeader(http.StatusPartialContent)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case failRetry && start == rangeBlockSize+rangeRetrySize:
					http.Error(w, "retry failed", http.StatusServiceUnavailable)
				default:
					sendRange(w, data, start, end)
				}
			}))
			defer server.Close()
			cache := NewCache(t.TempDir(), 64<<20)
			d := cache.download("source", "track", "high", &music.Stream{
				URL: server.URL, Type: "m4a", Size: int64(len(data)), ByteRanges: true,
			})
			defer d.Close()
			d.mu.Lock()
			for !d.done {
				d.cond.Wait()
			}
			fallback, err := d.ranges.fallback, d.err
			d.mu.Unlock()
			if err != nil || fallback != failRetry {
				t.Fatalf("fallback=%v, error=%v", fallback, err)
			}
			path := filepath.Join(cache.dir, cacheKey("source", "track", "high")+".m4a")
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("cache data: %d bytes, %v", len(got), err)
			}
			header := func(start, end int) string { return fmt.Sprintf("bytes=%d-%d", start, end) }
			want := []string{header(0, rangeBlockSize-1), header(rangeBlockSize, 2*rangeBlockSize-1),
				header(rangeBlockSize, rangeBlockSize+rangeRetrySize-1),
				header(rangeBlockSize+rangeRetrySize, rangeBlockSize+2*rangeRetrySize-1)}
			if failRetry {
				want = append(want, fmt.Sprintf("bytes=%d-", rangeBlockSize))
			} else {
				want = append(want, header(rangeBlockSize+2*rangeRetrySize, rangeBlockSize+3*rangeRetrySize-1),
					header(rangeBlockSize+3*rangeRetrySize, 2*rangeBlockSize-1),
					header(2*rangeBlockSize, 3*rangeBlockSize-1), header(3*rangeBlockSize, len(data)-1))
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(requests, want) {
				t.Fatalf("requests = %q, want %q", requests, want)
			}
		})
	}
}

func sendRange(w http.ResponseWriter, data []byte, start, end int) {
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
	w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
	w.WriteHeader(http.StatusPartialContent)
	w.Write(data[start : end+1])
}

// A seek should pass an in-flight cache fill and write the block that both the
// decoder and the eventual complete cache will use.
func TestRangeSeekSharesDownloadWithCache(t *testing.T) {
	for _, disk := range []bool{true, false} {
		t.Run(fmt.Sprintf("disk=%v", disk), func(t *testing.T) {
			data := rangeFixture()
			front, resume := make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			requests := make(map[int]int)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var start, end int
				if n, _ := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); n != 2 {
					t.Error("unexpected sequential request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				mu.Lock()
				requests[start]++
				first := requests[start] == 1
				mu.Unlock()
				if start == 0 && first {
					close(front)
					select {
					case <-resume:
					case <-r.Context().Done():
						return
					}
				}
				sendRange(w, data, start, end)
			}))
			defer server.Close()
			info := &music.Stream{URL: server.URL, Type: "m4a", Size: int64(len(data)), ByteRanges: true}
			var d *download
			var cache *Cache
			if disk {
				cache = NewCache(t.TempDir(), 64<<20)
				d = cache.download("source", "track", "high", info)
			} else {
				d = startDownload(info, nil, nil)
			}
			defer d.Close()
			select {
			case <-front:
			case <-time.After(3 * time.Second):
				t.Fatal("initial block was not requested")
			}
			f := &downloadFile{dl: d}
			for _, offset := range []int64{2 * rangeBlockSize, 2*rangeBlockSize + 12} {
				got := make([]byte, 48)
				if n, err := f.ReadAt(got, offset); n != len(got) || err != nil || !bytes.Equal(got, data[offset:offset+int64(len(got))]) {
					t.Fatalf("seek read at %d: %d bytes, %v", offset, n, err)
				}
			}
			if progress := d.Progress(); progress <= 0 || progress >= 1 {
				t.Fatalf("progress with only a sparse block = %f", progress)
			}
			if disk && cache.has("source", "track", "high") {
				t.Fatal("published an incomplete cache file")
			}
			close(resume)
			d.mu.Lock()
			for !d.done {
				d.cond.Wait()
			}
			err := d.err
			d.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(data))
			if n, err := f.ReadAt(got, 0); n != len(got) || err != nil || !bytes.Equal(got, data) {
				t.Fatalf("full shared read: %d bytes, %v", n, err)
			}
			if disk {
				path := filepath.Join(cache.dir, cacheKey("source", "track", "high")+".m4a")
				kept, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(kept, data) {
					t.Fatalf("full cache: %d bytes, %v", len(kept), err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for start := 0; start < len(data); start += rangeBlockSize {
				if requests[start] != 1 {
					t.Errorf("block %d fetched %d times", start, requests[start])
				}
			}
		})
	}
}

// A server that ignores Range must still work, even after a prior 206.
func TestRangeDownloadFallsBackToSequential(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprintf("accepted-first=%v", accepted), func(t *testing.T) {
			data := rangeFixture()
			var mu sync.Mutex
			var ranges, full int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var start, end int
				if n, _ := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); n == 2 {
					mu.Lock()
					ranges++
					mu.Unlock()
					if accepted && start == 0 {
						sendRange(w, data, start, end)
					} else {
						w.WriteHeader(http.StatusOK)
					}
					return
				}
				mu.Lock()
				full++
				mu.Unlock()
				w.Write(data)
			}))
			defer server.Close()
			cache := NewCache(t.TempDir(), 64<<20)
			d := cache.download("source", "track", "high", &music.Stream{
				URL: server.URL, Type: "m4a", Size: int64(len(data)), ByteRanges: true,
			})
			defer d.Close()
			f := &downloadFile{dl: d}
			got := make([]byte, len(data))
			if n, err := f.ReadAt(got, 0); n != len(got) || err != nil || !bytes.Equal(got, data) {
				t.Fatalf("fallback read: %d bytes, %v", n, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if ranges == 0 || full != 1 {
				t.Fatalf("range requests = %d, sequential requests = %d", ranges, full)
			}
		})
	}
}
