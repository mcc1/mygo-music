package player

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"mygo-music/internal/music"
)

const (
	rangeMinGap    = 16 << 10
	rangeBlockSize = 64 << 10
	rangeTimeout   = 4 * time.Second
)

type audioRangeBlock struct {
	start int64
	data  []byte
}

// audioRanges keeps two blocks separate from the sequential download. Sparse
// reads never advance its progress or make an incomplete song cacheable.
type audioRanges struct {
	ctx     context.Context
	url     string
	size    int64
	headers map[string]string
	client  *http.Client

	mu       sync.Mutex
	blocks   [2]audioRangeBlock
	next     int
	disabled bool
}

func newAudioRanges(ctx context.Context, info *music.Stream) *audioRanges {
	u, err := url.Parse(info.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	return &audioRanges{
		ctx: ctx, url: info.URL, size: info.Size, headers: info.Headers,
		client: music.HeadersStay(http.DefaultClient, u.Hostname(), info.Headers),
	}
}

func (r *audioRanges) readAt(b []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disabled || off < 0 || off >= r.size || int64(len(b)) > r.size-off {
		return 0, io.ErrUnexpectedEOF
	}
	total := 0
	for len(b) > 0 {
		start := off / rangeBlockSize * rangeBlockSize
		block, err := r.block(start)
		if err != nil {
			r.disabled = true // let the sequential download satisfy this and later reads
			return total, err
		}
		n := copy(b, block[off-start:])
		total += n
		off += int64(n)
		b = b[n:]
	}
	return total, nil
}

func (r *audioRanges) block(start int64) ([]byte, error) {
	for _, block := range r.blocks {
		if block.data != nil && block.start == start {
			return block.data, nil
		}
	}
	length := min(int64(rangeBlockSize), r.size-start)
	end := start + length - 1
	ctx, cancel := context.WithTimeout(r.ctx, rangeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", music.Agent)
	for name, value := range r.headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	res, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	contentRange := fmt.Sprintf("bytes %d-%d/%d", start, end, r.size)
	encoding := res.Header.Get("Content-Encoding")
	if res.StatusCode != http.StatusPartialContent ||
		res.Header.Get("Content-Range") != contentRange ||
		(encoding != "" && !strings.EqualFold(encoding, "identity")) ||
		(res.ContentLength >= 0 && res.ContentLength != length) {
		return nil, fmt.Errorf("audio range not honored (HTTP %d)", res.StatusCode)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(res.Body, data); err != nil {
		return nil, err
	}
	r.blocks[r.next] = audioRangeBlock{start: start, data: data}
	r.next = (r.next + 1) % len(r.blocks)
	return data, nil
}
