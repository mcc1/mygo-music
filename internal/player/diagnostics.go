package player

// PlaybackDiagnostics describes what the decoder and download are doing.
// It contains only format and progress data.
type PlaybackDiagnostics struct {
	Container, Codec, Processing  string
	Resolver                      string
	Transfer                      string
	Received, Total               int64
	Complete                      bool
	BitRate, SampleRate, Channels int
	EstimatedBitRate              bool
}

func (p *Player) Diagnostics() (PlaybackDiagnostics, bool) {
	var result PlaybackDiagnostics
	if p.stream == nil {
		if p.remote != nil && p.remote.started {
			result.Transfer = "remote"
			return result, true
		}
		return result, false
	}
	if p.info != nil {
		result.Container, result.Codec, result.Processing = p.info.Type, p.info.Codec, p.info.Processing
		result.Resolver = p.info.Resolver
		result.BitRate = p.info.BitRate
	}
	result.SampleRate, result.Channels = p.stream.SampleRate(), p.stream.Channels()
	switch s := p.stream.(type) {
	case *mp3Stream:
		result.Container, result.Codec = "mp3", "mp3"
	case *oggStream:
		result.Container, result.Codec = "ogg", "vorbis"
	case *adtsStream:
		result.Container = "adts"
		if result.Codec == "" {
			result.Codec = "aac"
		}
	case *wavStream:
		result.Container, result.Codec = "wav", "pcm"
		if s.format.float {
			result.Codec = "pcm-float"
		}
	case *decodedStream:
		if _, ok := s.Decoder.(*opusDecoder); ok {
			result.Container, result.Codec = "ogg", "opus"
		}
	}

	dl, _ := p.stream.source()
	dl.mu.Lock()
	defer dl.mu.Unlock()
	result.Received, result.Total = dl.n, dl.size
	result.Complete = dl.done && dl.err == nil
	size := dl.size
	if result.Complete || dl.onDisk {
		size = dl.n
	}
	if seconds := p.Duration().Seconds(); result.BitRate <= 0 && size > 0 && seconds > 0 {
		result.BitRate = int(float64(size) * 8 / seconds)
		result.EstimatedBitRate = true
	}
	switch {
	case dl.cached:
		result.Transfer = "cache"
	case dl.onDisk:
		result.Transfer = "file"
	case dl.ranges != nil && !dl.ranges.fallback:
		result.Transfer = "range"
		result.Received = dl.ranges.received
	case dl.ranges != nil:
		result.Transfer = "range-fallback"
	default:
		result.Transfer = "sequential"
	}
	return result, true
}
