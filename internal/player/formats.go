package player

import "slices"

var progressiveCodecs = map[string][]string{
	"mp4":  {"aac-lc", "alac", "flac"},
	"adts": {"aac-lc"},
	"mp3":  {"mp3"},
	"flac": {"flac"},
	"ogg":  {"vorbis"},
	"wav":  {"pcm", "pcm-float"},
}

// SupportsAudio reports which container and codec pairs openStream can decode
// from a single progressive HTTP response. The container must have its
// headers, and any required index, before the audio. Playlist manifests need
// a separate reader.
func SupportsAudio(container, codec, delivery string) bool {
	return delivery == "progressive" && slices.Contains(progressiveCodecs[container], codec)
}
