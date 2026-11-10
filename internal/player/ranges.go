package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mygo-music/internal/music"
)

const (
	rangeBlockSize = 1 << 20
	rangeRetrySize = 256 << 10
	rangeTimeout   = 4 * time.Second
)

// audioRanges shares one download between playback and the cache. All mutable
// state is guarded by the download's mutex. The contiguous prefix lives in the
// usual file or buffer; blocks beyond it are tracked separately until joined.
type audioRanges struct {
	ctx     context.Context
	cancel  context.CancelFunc
	url     string
	size    int64
	headers map[string]string
	client  *http.Client

	ready    map[int64][]byte // nil value for a block already written to disk
	pending  map[int64]bool   // queued or in flight, across both workers
	urgent   []int64
	received int64
	etag     string
	fallback bool
}

func newAudioRanges(ctx context.Context, info *music.Stream) *audioRanges {
	u, err := url.Parse(info.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	return &audioRanges{
		ctx: ctx, cancel: cancel, url: info.URL, size: info.Size, headers: info.Headers,
		client: music.HeadersStay(http.DefaultClient, u.Hostname(), info.Headers),
		ready:  make(map[int64][]byte), pending: make(map[int64]bool),
	}
}

// downloadRanges fills the cache in order while another worker can fetch a
// block needed for playback without waiting for the background request.
func (d *download) downloadRanges() bool {
	r := d.ranges
	defer r.cancel()
	priorityDone := make(chan struct{})
	go func() {
		defer close(priorityDone)
		d.rangeWorker(true)
	}()
	d.rangeWorker(false)
	<-priorityDone

	d.mu.Lock()
	complete, fallback := !d.done && d.n == r.size, r.fallback
	d.mu.Unlock()
	if complete && d.part != "" {
		d.hand()
	}
	return !fallback
}

func (d *download) rangeWorker(priority bool) {
	r := d.ranges
	d.mu.Lock()
	defer d.mu.Unlock()
	for !d.done && !r.fallback && d.n < r.size {
		var start int64
		if priority {
			if len(r.urgent) == 0 {
				d.cond.Wait()
				continue
			}
			start, r.urgent = r.urgent[0], r.urgent[1:]
		} else {
			start = d.n
			if len(r.urgent) > 0 || r.pending[start] {
				d.cond.Wait()
				continue
			}
			r.pending[start] = true
		}
		d.mu.Unlock()
		data, etag, err := r.fetch(start)
		d.mu.Lock()
		if d.done || r.fallback {
			return
		}
		if err != nil || (r.etag != "" && etag != r.etag) || !d.storeRange(start, data) {
			d.restartSequential()
			return
		}
		if etag != "" {
			r.etag = etag
		}
		delete(r.pending, start)
		d.cond.Broadcast()
	}
}

// storeRange publishes a block only after writing it completely.
func (d *download) storeRange(start int64, data []byte) bool {
	r := d.ranges
	r.received += int64(len(data))
	if d.file != nil {
		if _, err := d.file.WriteAt(data, start); err != nil {
			return false
		}
		data = nil
	}
	r.ready[start] = data
	for {
		data, ok := r.ready[d.n]
		if !ok {
			return true
		}
		if d.file == nil {
			d.data = append(d.data, data...)
		}
		delete(r.ready, d.n)
		d.n += min(int64(rangeBlockSize), r.size-d.n)
	}
}

// restartSequential keeps the contiguous prefix. Neither worker can publish
// another block once fallback is set.
func (d *download) restartSequential() {
	r := d.ranges
	r.fallback = true
	r.cancel()
	r.ready, r.pending, r.urgent = nil, nil, nil
	d.cond.Broadcast()
}

// readRanges prioritizes missing blocks and waits for the same request other
// readers or the cache may already have started. It only copies data once
// the whole read is available, so fallback can retry it as a single read.
func (d *download) readRanges(b []byte, off int64) (int, error, bool) {
	r := d.ranges
	d.mu.Lock()
	defer d.mu.Unlock()
	end := off + int64(len(b))
	for start := off / rangeBlockSize * rangeBlockSize; start < end; start += min(int64(rangeBlockSize), r.size-start) {
		for !r.fallback {
			_, ready := r.ready[start]
			if start < d.n || ready {
				break
			}
			if d.done {
				return 0, d.err, false
			}
			if !r.pending[start] {
				r.pending[start] = true
				r.urgent = append(r.urgent, start)
				d.cond.Broadcast()
			}
			d.cond.Wait()
		}
		if r.fallback {
			return 0, nil, false
		}
	}
	if d.file != nil {
		n, err := d.file.ReadAt(b, off)
		return n, err, true
	}
	n, _ := d.readAt(b, off)
	for n < len(b) {
		at := off + int64(n)
		start := at / rangeBlockSize * rangeBlockSize
		n += copy(b[n:], r.ready[start][at-start:])
	}
	return n, nil, true
}

func (r *audioRanges) fetch(start int64) ([]byte, string, error) {
	length := min(int64(rangeBlockSize), r.size-start)
	data := make([]byte, length)
	etag, err := r.fetchInto(start, data)
	if !errors.Is(err, context.DeadlineExceeded) || r.ctx.Err() != nil || length <= rangeRetrySize {
		return data, etag, err
	}
	for offset := int64(0); offset < length; offset += rangeRetrySize {
		part := data[offset:min(offset+rangeRetrySize, length)]
		tag, err := r.fetchInto(start+offset, part)
		if err != nil {
			return nil, "", err
		}
		if etag != "" && tag != etag {
			return nil, "", errors.New("audio changed during range retry")
		}
		etag = tag
	}
	return data, etag, nil
}

func (r *audioRanges) fetchInto(start int64, data []byte) (string, error) {
	length := int64(len(data))
	end := start + length - 1
	ctx, cancel := context.WithTimeout(r.ctx, rangeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", music.Agent)
	for name, value := range r.headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Del("If-Range")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	res, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if !validAudioRange(res, start, end, r.size) {
		return "", fmt.Errorf("audio range not honored (HTTP %d)", res.StatusCode)
	}
	_, err = io.ReadFull(res.Body, data)
	return res.Header.Get("ETag"), err
}

func validAudioRange(res *http.Response, start, end, size int64) bool {
	encoding := res.Header.Get("Content-Encoding")
	return res.StatusCode == http.StatusPartialContent &&
		res.Header.Get("Content-Range") == fmt.Sprintf("bytes %d-%d/%d", start, end, size) &&
		(encoding == "" || strings.EqualFold(encoding, "identity")) &&
		(res.ContentLength < 0 || res.ContentLength == end-start+1)
}

// Try to finish with one sequential response from the end of the prefix.
// A 200 response can still be used after comparing its prefix.
func (r *audioRanges) resume(client *http.Client, req *http.Request, start int64) (*http.Response, error) {
	next := req.Clone(req.Context())
	next.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
	if strings.HasPrefix(r.etag, "\"") {
		next.Header.Set("If-Range", r.etag)
	}
	res, err := client.Do(next)
	if err == nil {
		if res.StatusCode == http.StatusOK ||
			(validAudioRange(res, start, r.size-1, r.size) && (r.etag == "" || res.Header.Get("ETag") == r.etag)) {
			return res, nil
		}
		res.Body.Close()
	}
	res, err = client.Do(req)
	if err == nil && res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("audio sequential fallback failed (HTTP %d)", res.StatusCode)
	}
	return res, err
}
