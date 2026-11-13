package app

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/player"
	"mygo-music/internal/speech"
)

func (a *app) debugSettings(c *ui.Context, row settingsRow, block settingsBlock) {
	row(T("Playback Diagnostics"), T("Show the audio format, playback route and download status in the player"), func() {
		if ui.Switch(c.Key("playback-diagnostics"), &a.settings.PlaybackDiagnostics).Label(T("Playback Diagnostics")).Changed() {
			a.changed()
		}
	})
	if !speech.WhisperAvailable() {
		return
	}
	label := T("Whisper Backend")
	row(label, T("Automatic tries CUDA, Vulkan, then CPU. A specific backend disables fallback."), func() {
		keys := []string{"", "cuda", "vulkan", "cpu"}
		labels := []string{"Automatic", "CUDA", "Vulkan", "CPU"}
		if i, changed := choice(c, "whisper-backend", label, labels, slices.Index(keys, a.settings.WhisperBackend)); changed {
			a.settings.WhisperBackend = keys[i]
			a.captions.whisperUsed, a.captions.whisperError = "", false
			if a.captionModel() == speech.WhisperModelName {
				a.stopCaptions()
				a.captions.failed = ""
				a.syncCaptions()
			}
			a.changed()
		}
	})
	status := T("Waiting for Whisper to transcribe")
	if a.captions.whisperError {
		status = T("Whisper transcription failed")
	} else if used := a.captions.whisperUsed; used != "" {
		status = Tf("Whisper used %s", strings.ToUpper(used))
	}
	block(status, func() { ui.Text(c, status).FontSize(13) })
}

func (a *app) playbackDiagnostics(c *ui.Context, panel ui.Element) {
	d, ok := a.pl.Diagnostics()
	lines := []string{T("Waiting for stream details")}
	if ok {
		lines = playbackDetails(d)
		if !d.Complete && !a.pl.Playing() && (d.Transfer == "range" || d.Transfer == "range-fallback" || d.Transfer == "sequential") {
			c.After(time.Second)
		}
	}
	panel.Width(196).Padding(8, 11).Gap(3).Radius(8).
		Background(ui.RGB(20, 20, 23).Alpha(0.88)).Border(1, white.Alpha(0.14)).PassThrough().Children(func() {
		ui.Text(c, T("Playback Diagnostics")).FontSize(12).FontWeight(600).TextColor(white)
		for _, line := range lines {
			ui.Text(c, line).FontSize(11.5).TextColor(white.Alpha(0.85))
		}
	})
}

func playbackDetails(d player.PlaybackDiagnostics) []string {
	var parts []string
	if slices.Contains([]string{"m4a", "aac", "adts", "ogg", "mp3", "flac", "wav", "webm"}, d.Container) {
		parts = append(parts, Tf("Container %s", strings.ToUpper(d.Container)))
	}
	if slices.Contains([]string{"opus", "vorbis", "aac", "aac-lc", "he-aac", "alac", "flac", "pcm", "pcm-float", "mp3"}, d.Codec) {
		parts = append(parts, Tf("Codec %s", strings.ToUpper(d.Codec)))
	}
	if d.BitRate > 0 {
		label := "Bitrate %.0f kbps"
		if d.EstimatedBitRate {
			label = "Estimated bitrate %.0f kbps"
		}
		parts = append(parts, Tf(label, float64(d.BitRate)/1000))
	}
	if d.SampleRate > 0 && d.Channels > 0 {
		parts = append(parts, Tf("Decoded %d Hz / %d ch", d.SampleRate, d.Channels))
	}
	switch d.Processing {
	case "direct":
		parts = append(parts, T("Native playback"))
	case "remux":
		parts = append(parts, T("FFmpeg remux"))
	case "transcode":
		parts = append(parts, T("FFmpeg transcode"))
	}
	if d.Resolver == "anonymous-yt-dlp" {
		parts = append(parts, T("Anonymous yt-dlp"))
	}
	switch d.Transfer {
	case "cache":
		parts = append(parts, T("Cache hit"))
	case "file":
		parts = append(parts, T("Local file"))
	case "remote":
		parts = append(parts, T("Remote playback"))
	default:
		switch d.Transfer {
		case "range":
			parts = append(parts, "HTTP Range")
		case "range-fallback":
			parts = append(parts, T("Range fallback to sequential"))
		default:
			parts = append(parts, T("Sequential download"))
		}
		switch {
		case d.Complete:
			parts = append(parts, T("Download complete"))
		case d.Total > 0:
			parts = append(parts, Tf("Downloaded %d%%", min(100, int(100*float64(d.Received)/float64(d.Total)))))
		default:
			parts = append(parts, Tf("Received %.1f MiB", float64(d.Received)/(1<<20)))
		}
	}
	return parts
}
