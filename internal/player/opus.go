package player

import (
	"encoding/binary"
	"errors"
	"io"
	"math"

	"github.com/tphakala/go-opus/oggopus"
)

// openOggAudio identifies Opus by its first packet. The Vorbis reader starts
// from the beginning when the first packet isn't Opus.
func openOggAudio(dl *download, durationMs int64) (stream, error) {
	reader := &downloadSeeker{downloadReader{d: dl}}
	r, err := oggopus.NewDecoder(reader)
	if errors.Is(err, oggopus.ErrNotOpus) {
		return openOggStream(dl, durationMs)
	}
	if err != nil {
		return nil, err
	}
	channels := r.Info().Channels
	if channels != 1 && channels != 2 {
		return nil, errors.New("opus: only mono and stereo are supported")
	}
	d := &opusDecoder{r: r, reader: reader, channels: channels, frames: -1}
	if durationMs > 0 {
		d.frames = durationMs * oggopus.OutputSampleRate / 1000
	}
	if d.whole() {
		if frames, err := r.Length(); err == nil {
			d.frames = frames
		} else if errors.Is(err, oggopus.ErrSeekUnsupported) {
			d.linearSeek = true
		}
	}
	return &decodedStream{Decoder: d, dl: dl, spec: newSpectrum(d.SampleRate()), drop: dl.Close}, nil
}

type opusDecoder struct {
	r          *oggopus.Decoder
	reader     *downloadSeeker
	channels   int
	frames     int64
	pos        int64
	linearSeek bool
	pcm        []byte
}

func (d *opusDecoder) SampleRate() int { return oggopus.OutputSampleRate }
func (d *opusDecoder) Channels() int   { return 2 }
func (d *opusDecoder) Len() int64      { return d.frames }

func (d *opusDecoder) Read(dst []float32) (int, error) {
	stride := d.channels * 2
	want := len(dst) / 2 * stride
	if want == 0 {
		return 0, nil
	}
	if cap(d.pcm) < want {
		d.pcm = make([]byte, want)
	}
	n, err := d.r.Read(d.pcm[:want])
	frames := n / stride
	d.pos += int64(frames)
	for i := range frames {
		frame := d.pcm[i*stride : (i+1)*stride]
		dst[2*i] = float32(int16(binary.LittleEndian.Uint16(frame))) / 32768
		dst[2*i+1] = float32(int16(binary.LittleEndian.Uint16(frame[stride-2:]))) / 32768
	}
	return frames * 2, err
}

func (d *opusDecoder) SetPosition(frame int64) error {
	frame = max(frame, 0)
	if frame > 0 && !d.linearSeek && d.whole() {
		pos, err := d.r.SeekToSample(frame)
		if err == nil {
			d.pos = pos
			return nil
		}
		if !errors.Is(err, oggopus.ErrSeekUnsupported) {
			return err
		}
		// Some remuxed files have a nonzero first granule offset. They
		// still decode sequentially, even without a usable seek index.
		d.linearSeek = true
	}
	if frame < d.pos || frame == 0 {
		if _, err := d.reader.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := d.r.Reset(d.reader); err != nil {
			return err
		}
		d.pos = 0
	}
	// For live downloads, decode only as far as the target so seeking
	// does not wait for the end of the relay.
	stride := int64(d.channels * 2)
	if frame-d.pos > math.MaxInt64/stride {
		return errors.New("opus: seek target too large")
	}
	n, err := io.CopyN(io.Discard, d.r, (frame-d.pos)*stride)
	d.pos += n / stride
	if err == io.EOF {
		return nil // a source's duration can extend past the last sample
	}
	return err
}

func (d *opusDecoder) whole() bool {
	dl := d.reader.d
	dl.mu.Lock()
	defer dl.mu.Unlock()
	return dl.done || dl.onDisk
}
