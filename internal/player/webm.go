package player

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"

	"github.com/tphakala/go-opus/opus"
)

const (
	webmHeader   = 0x1a45dfa3
	webmSegment  = 0x18538067
	webmSeekHead = 0x114d9b74
	webmInfo     = 0x1549a966
	webmTracks   = 0x1654ae6b
	webmCues     = 0x1c53bb6b
	webmCluster  = 0x1f43b675
	webmRate     = 48000
)

var errWebM = errors.New("webm: invalid or unsupported Opus stream")

type webmElement struct {
	id         uint64
	start, end int64
}

// EBML IDs retain the width marker; sizes, block track numbers and lacing
// values strip it. A size with all its data bits set is unknown.
func webmVint(b []byte, id bool) (uint64, int) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0
	}
	mask, n := byte(0x80), 1
	for b[0]&mask == 0 {
		mask >>= 1
		n++
	}
	if len(b) < n || (id && n > 4) {
		return 0, 0
	}
	v := uint64(b[0])
	if !id {
		v &= uint64(mask - 1)
	}
	for _, x := range b[1:n] {
		v = v<<8 | uint64(x)
	}
	return v, n
}

type webmFile struct{ io.ReaderAt }

func (f webmFile) element(off, limit int64) (webmElement, error) {
	if off >= limit {
		return webmElement{}, io.EOF
	}
	// Read just the element header, even when the content spans more ranges.
	// A short read at the end may still contain a valid empty element.
	var b [12]byte
	n, err := f.ReadAt(b[:min(int64(len(b)), limit-off)], off)
	id, a := webmVint(b[:n], true)
	if a == 0 {
		if err != nil {
			return webmElement{}, err
		}
		return webmElement{}, errWebM
	}
	size, width := webmVint(b[a:n], false)
	if width == 0 {
		return webmElement{}, errWebM
	}
	start := off + int64(a+width)
	if size == 1<<(7*width)-1 {
		if id != webmSegment || limit == math.MaxInt64 {
			return webmElement{}, errWebM
		}
		return webmElement{id, start, limit}, nil
	}
	if size > uint64(limit-start) {
		return webmElement{}, errWebM
	}
	return webmElement{id, start, start + int64(size)}, nil
}

func (f webmFile) each(parent webmElement, visit func(webmElement) error) error {
	for off := parent.start; off < parent.end; {
		e, err := f.element(off, parent.end)
		if err != nil {
			return err
		}
		if err := visit(e); err != nil {
			return err
		}
		off = e.end
	}
	return nil
}

func (f webmFile) bytes(e webmElement, maxSize int64) ([]byte, error) {
	size := e.end - e.start
	if size <= 0 || size > maxSize {
		return nil, errWebM
	}
	b := make([]byte, size)
	_, err := f.ReadAt(b, e.start)
	return b, err
}

func (f webmFile) number(e webmElement) (uint64, error) {
	var b [8]byte
	size := e.end - e.start
	if size == 0 {
		return 0, nil
	}
	if size > 8 {
		return 0, errWebM
	}
	_, err := f.ReadAt(b[8-size:], e.start)
	return binary.BigEndian.Uint64(b[:]), err
}

type webmCue struct {
	tick   uint64
	offset int64
}

type webmOpus struct {
	webmFile
	segment           webmElement
	first, cueOffset  int64
	cues              []webmCue
	cuesRead          bool
	track, scale      uint64
	channels, preskip int
	gain              float32
	preroll, frames   int64

	decoder         *opus.Decoder
	clusterEnd, off int64
	tick            uint64
	packets         [][]byte
	discard         int64
	target          int64
	align           bool
	skip            int64
	pcm             []int16
	samples         []int16
}

func openWebmOpus(dl *download, durationMs int64) (stream, error) {
	dl.mu.Lock()
	size := dl.size
	if dl.done || dl.onDisk {
		size = dl.n
	}
	dl.mu.Unlock()
	if size <= 0 {
		size = math.MaxInt64
	}
	f := webmFile{&downloadFile{dl: dl}}
	head, err := f.element(0, size)
	if err != nil || head.id != webmHeader {
		return nil, errWebM
	}
	segment, err := f.element(head.end, size)
	if err != nil || segment.id != webmSegment {
		return nil, errWebM
	}
	d := &webmOpus{webmFile: f, segment: segment, scale: 1000000, preroll: 3840, frames: -1}
	var duration float64
	if durationMs > 0 && durationMs <= math.MaxInt64/48 {
		d.frames = durationMs * 48
	}
	for off := segment.start; off < segment.end && d.first == 0; {
		e, err := f.element(off, segment.end)
		if err != nil {
			return nil, err
		}
		switch e.id {
		case webmSeekHead:
			err = d.seekHead(e)
		case webmInfo:
			err = f.each(e, func(child webmElement) error {
				switch child.id {
				case 0x2ad7b1: // TimestampScale, in ns
					d.scale, err = f.number(child)
					return err
				case 0x4489: // Duration, in ticks
					bits, err := f.number(child)
					switch child.end - child.start {
					case 4:
						duration = float64(math.Float32frombits(uint32(bits)))
					case 8:
						duration = math.Float64frombits(bits)
					}
					return err
				}
				return nil
			})
		case webmTracks:
			err = d.readTracks(e)
		case webmCues:
			d.cueOffset = off
		case webmCluster:
			d.first = off
		}
		if err != nil {
			return nil, err
		}
		off = e.end
	}
	if d.first == 0 || d.track == 0 || d.scale == 0 || d.scale > 1000000000 {
		return nil, errWebM
	}
	if ns := duration * float64(d.scale); d.frames < 0 && ns > 0 && ns < math.MaxInt64 {
		d.frames = webmNSFrames(int64(ns))
	}
	d.decoder, err = opus.NewDecoder(webmRate, d.channels)
	if err != nil {
		return nil, err
	}
	d.pcm = make([]int16, 5760*d.channels) // up to 120 ms per Opus packet
	d.reset(d.first, 0)
	if err := d.fill(); err != nil {
		return nil, err
	}
	return &decodedStream{Decoder: d, dl: dl, spec: newSpectrum(webmRate), drop: dl.Close}, nil
}

func (d *webmOpus) readTracks(e webmElement) error {
	return d.each(e, func(entry webmElement) error {
		if entry.id != 0xae || d.track != 0 {
			return nil
		}
		var track, typ, preroll uint64
		var codec string
		var head []byte
		err := d.each(entry, func(child webmElement) error {
			var err error
			switch child.id {
			case 0xd7:
				track, err = d.number(child)
			case 0x83:
				typ, err = d.number(child)
			case 0x86:
				var b []byte
				b, err = d.bytes(child, 128)
				codec = string(b)
			case 0x63a2:
				// Other tracks may contain much larger codec private data.
				if child.end-child.start == 19 {
					head, err = d.bytes(child, 19)
				}
			case 0x56bb:
				preroll, err = d.number(child)
			}
			return err
		})
		if err != nil || typ != 2 || codec != "A_OPUS" {
			return err
		}
		if track == 0 || len(head) != 19 || string(head[:8]) != "OpusHead" ||
			head[8] > 15 || (head[9] != 1 && head[9] != 2) || head[18] != 0 {
			return errWebM
		}
		d.track, d.channels = track, int(head[9])
		d.preskip = int(binary.LittleEndian.Uint16(head[10:12]))
		db := float64(int16(binary.LittleEndian.Uint16(head[16:18]))) / 256
		d.gain = float32(math.Pow(10, db/20)) / 32768
		if preroll > 0 && preroll <= math.MaxInt64 {
			d.preroll = webmNSFrames(int64(preroll))
		}
		return nil
	})
}

func (d *webmOpus) seekHead(head webmElement) error {
	return d.each(head, func(seek webmElement) error {
		if seek.id != 0x4dbb {
			return nil
		}
		var id, pos uint64
		err := d.each(seek, func(e webmElement) error {
			var err error
			switch e.id {
			case 0x53ab:
				id, err = d.number(e)
			case 0x53ac:
				pos, err = d.number(e)
			}
			return err
		})
		if id == webmCues && pos < uint64(d.segment.end-d.segment.start) {
			d.cueOffset = d.segment.start + int64(pos)
		}
		return err
	})
}

func (d *webmOpus) readCues() {
	d.cuesRead = true
	if d.cueOffset == 0 {
		return
	}
	cues, err := d.element(d.cueOffset, d.segment.end)
	if err != nil || cues.id != webmCues || cues.end-cues.start > 8<<20 {
		return
	}
	err = d.each(cues, func(point webmElement) error {
		if point.id != 0xbb {
			return nil
		}
		var tick uint64
		var offset int64
		err := d.each(point, func(e webmElement) error {
			switch e.id {
			case 0xb3:
				var err error
				tick, err = d.number(e)
				return err
			case 0xb7:
				var track, pos uint64
				err := d.each(e, func(field webmElement) error {
					var err error
					switch field.id {
					case 0xf7:
						track, err = d.number(field)
					case 0xf1:
						pos, err = d.number(field)
					}
					return err
				})
				if err == nil && track == d.track && pos < uint64(d.segment.end-d.segment.start) {
					offset = d.segment.start + int64(pos)
				}
				return err
			}
			return nil
		})
		if err == nil && offset >= d.first {
			d.cues = append(d.cues, webmCue{tick, offset})
		}
		return err
	})
	if err != nil {
		d.cues = nil
	}
	slices.SortFunc(d.cues, func(a, b webmCue) int {
		if a.tick < b.tick {
			return -1
		}
		if a.tick > b.tick {
			return 1
		}
		return 0
	})
}

func webmNSFrames(ns int64) int64 {
	return ns/1000000000*webmRate + int64(math.Round(float64(ns%1000000000)*webmRate/1000000000))
}

func (d *webmOpus) tickFrame(tick uint64) int64 {
	if tick > uint64(math.MaxInt64)/d.scale {
		return webmNSFrames(math.MaxInt64)
	}
	return webmNSFrames(int64(tick * d.scale))
}

func (d *webmOpus) reset(off, frame int64) {
	d.decoder.Reset()
	d.off, d.clusterEnd = off, off
	d.tick, d.discard, d.skip = 0, 0, 0
	d.packets, d.samples = nil, nil
	d.target, d.align = frame, true
}

func (d *webmOpus) SampleRate() int { return webmRate }
func (d *webmOpus) Channels() int   { return 2 }
func (d *webmOpus) Len() int64      { return d.frames }

func (d *webmOpus) SetPosition(frame int64) error {
	frame = max(frame, 0)
	if d.frames >= 0 {
		frame = min(frame, d.frames)
	}
	offset := d.first
	if frame > 0 {
		if !d.cuesRead {
			d.readCues()
		}
		// Decode from before the target to warm up Opus after a seek.
		before := max(frame-d.preroll, 0)
		for _, cue := range d.cues {
			if d.tickFrame(cue.tick)-int64(d.preskip) > before {
				break
			}
			offset = cue.offset
		}
	}
	d.reset(offset, frame)
	if err := d.fill(); err != io.EOF {
		return err
	}
	return nil
}

func (d *webmOpus) Read(dst []float32) (int, error) {
	written := 0
	for written+1 < len(dst) {
		if err := d.fill(); err != nil {
			return written, err
		}
		frames := min(len(d.samples)/d.channels, (len(dst)-written)/2)
		for i := range frames {
			at := i * d.channels
			dst[written+2*i] = float32(d.samples[at]) * d.gain
			dst[written+2*i+1] = float32(d.samples[at+d.channels-1]) * d.gain
		}
		written += 2 * frames
		d.samples = d.samples[frames*d.channels:]
	}
	return written, nil
}

func (d *webmOpus) fill() error {
	for len(d.samples) == 0 {
		if len(d.packets) == 0 {
			if err := d.nextBlock(); err != nil {
				return err
			}
		}
		packet := d.packets[0]
		d.packets = d.packets[1:]
		n, err := d.decoder.Decode(packet, d.pcm)
		if err != nil {
			return err
		}
		drop := min(d.skip, int64(n))
		d.skip -= drop
		if d.discard < 0 {
			drop = max(drop, min(-d.discard, int64(n)))
			d.discard = 0
		}
		if len(d.packets) == 0 && d.discard > 0 {
			n -= int(min(d.discard, int64(n)))
		}
		d.samples = d.pcm[min(int(drop), n)*d.channels : n*d.channels]
	}
	return nil
}

// A Matroska block can carry one packet or a lace of packets. The lace
// encodes lengths of all but its final packet.
func webmPackets(data []byte, lace byte) ([][]byte, error) {
	if len(data) == 0 {
		return nil, errWebM
	}
	if lace == 0 {
		return [][]byte{data}, nil
	}
	count := int(data[0]) + 1
	data = data[1:]
	lengths := make([]int, count)
	remaining := len(data)
	for i := range lengths[:count-1] {
		switch lace {
		case 2: // Xiph lacing
			for {
				if len(data) == 0 {
					return nil, errWebM
				}
				x := int(data[0])
				data = data[1:]
				remaining--
				lengths[i] += x
				if x < 255 {
					break
				}
			}
		case 4: // Fixed-size lacing
			if len(data)%count != 0 {
				return nil, errWebM
			}
			lengths[i] = len(data) / count
		case 6: // EBML lacing uses signed deltas after the first length.
			v, n := webmVint(data, false)
			if n == 0 {
				return nil, errWebM
			}
			size := int64(v)
			if i != 0 {
				size += int64(lengths[i-1]) - ((1 << (7*n - 1)) - 1)
			}
			if size <= 0 || size > int64(len(data)) {
				return nil, errWebM
			}
			lengths[i] = int(size)
			data = data[n:]
			remaining -= n
		}
		remaining -= lengths[i]
		if lengths[i] <= 0 || remaining <= 0 {
			return nil, errWebM
		}
	}
	lengths[count-1] = remaining
	packets := make([][]byte, count)
	for i, n := range lengths {
		if n <= 0 || n > len(data) {
			return nil, errWebM
		}
		packets[i], data = data[:n], data[n:]
	}
	return packets, nil
}

func (d *webmOpus) nextBlock() error {
	for d.off < d.segment.end {
		if d.off >= d.clusterEnd {
			e, err := d.element(d.off, d.segment.end)
			if err != nil {
				return err
			}
			d.off = e.end
			if e.id == webmCluster {
				d.off, d.clusterEnd, d.tick = e.start, e.end, 0
			}
			continue
		}
		e, err := d.element(d.off, d.clusterEnd)
		if err != nil {
			return err
		}
		d.off = e.end
		var block webmElement
		var discard int64
		switch e.id {
		case 0xe7: // Cluster Timestamp
			d.tick, err = d.number(e)
		case 0xa3: // SimpleBlock
			block = e
		case 0xa0: // BlockGroup, with optional end padding
			err = d.each(e, func(field webmElement) error {
				switch field.id {
				case 0xa1:
					block = field
				case 0x75a2:
					b, err := d.bytes(field, 8)
					if err != nil {
						return err
					}
					v := int64(int8(b[0]))
					for _, x := range b[1:] {
						v = v<<8 | int64(x)
					}
					discard = webmNSFrames(v)
				}
				return nil
			})
		}
		if err != nil {
			return err
		}
		if block.id == 0 {
			continue
		}
		b, err := d.bytes(block, 1<<20)
		if err != nil {
			return err
		}
		track, n := webmVint(b, false)
		if n == 0 || len(b) < n+4 {
			return errWebM
		}
		if track != d.track {
			continue
		}
		packets, err := webmPackets(b[n+3:], b[n+2]&6)
		if err != nil {
			return err
		}
		if d.align {
			relative := int64(int16(binary.BigEndian.Uint16(b[n : n+2])))
			tick := int64(d.tick)
			if d.tick > math.MaxInt64 || tick > math.MaxInt64-max(relative, 0) {
				return errWebM
			}
			start := d.tickFrame(uint64(max(tick+relative, 0))) - int64(d.preskip)
			d.skip = max(d.target-start, 0)
			d.align = false
		}
		d.packets, d.discard = packets, discard
		return nil
	}
	return io.EOF
}
