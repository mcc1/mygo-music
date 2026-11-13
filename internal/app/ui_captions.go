package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/i18n"
	"mygo-music/internal/lyrics"
	"mygo-music/internal/music"
	"mygo-music/internal/speech"
)

// captionsPane shows the episode playing's captions as lyrics show, in
// Now Playing: written down on the device as it plays. Without the model,
// it offers to download it.
func (a *app) captionsPane(c *ui.Context, tr *music.Track, h float32) {
	pl := a.pl
	cs := &a.captions
	if !a.modelReady() {
		a.modelCard(c)
		return
	}
	if !a.settings.Captions {
		a.captionsNote(c, T("Captions are off"), T("Turn On Captions"), func() {
			a.settings.Captions = true
			a.settings.save()
			a.syncCaptions()
		})
		return
	}
	// Until the player says the episode changed, what the app has is the
	// last one's: none of it shows.
	var lines []lyrics.Line
	if cs.program == a.captionProgram(tr) {
		lines = cs.display.lines(cs.program, cs.lines, i18n.Language())
	}
	pos := pl.Position()
	if len(lines) > 0 {
		lines = a.translateCaptionLines(cs.program, lines, pos)
	}
	if len(lines) == 0 {
		switch {
		case cs.program != a.captionProgram(tr):
			a.captionsNote(c, T("Recognizing…"), "", nil)
		case cs.failed == cs.program:
			a.captionsNote(c, T("Couldn't make captions"), "", nil)
		case a.captionsWritten():
			a.captionsNote(c, T("No speech found in this episode"), "", nil)
		default:
			a.captionsNote(c, T("Recognizing…"), "", nil)
		}
		return
	}
	cur := lyrics.Index(lines, pos)
	// A line stays lit through a pause, but not where nothing is written
	// down yet: after a seek ahead, until the words there are.
	if cur >= 0 && pos >= lines[cur].End && a.captionsWriting(pos) {
		cur = -1
	}
	if pl.Playing() {
		if i := lyrics.Index(lines, pos); i+1 < len(lines) {
			c.After(lines[i+1].At - pos + 20*time.Millisecond)
		}
	}
	if a.lyricTrack != tr.ID {
		a.lyricTrack, a.lyricAt = tr.ID, -2
		a.lyricScroll, a.lyricGlide = ui.ScrollState{}, lyricGlide{}
	}
	lag := a.lyricGlide.step(c, &a.lyricScroll)
	scroll := ui.Scroll(c.Key("captions")).Grow(1).TrackScroll(&a.lyricScroll).Padding(0, 8, h*0.5, 0)
	a.timedLines(c, scroll, lines, pos, cur, lag, linesStyle{size: 24, weight: 700, lineHeight: 1.4}, func() {
		if a.settings.TranslateCaptions && a.translation.failed {
			ui.Text(c, T("Podcast translation failed. Toggle it in Settings to retry.")).FontSize(13).TextColor(white.Alpha(0.6)).Padding(16, 12)
		}
		if cs.work != nil {
			ui.Text(c, T("Recognizing…")).FontSize(14).FontWeight(600).TextColor(white.Alpha(0.35)).Padding(16, 12)
		}
	})
}

// captionsNote says how the captions are, in the middle of the pane, with
// a button if there is something to do.
func (a *app) captionsNote(c *ui.Context, text, button string, act func()) {
	ui.Column(c).Grow(1).Center().Gap(18).Children(func() {
		ui.Text(c, text).FontSize(22).FontWeight(700).TextColor(white.Alpha(0.4))
		if button != "" && pillButton(c.Key("act"), nil, button, white.Alpha(0.14), white).Clicked() {
			act()
		}
	})
}

// modelCard offers to download the model that writes captions, and shows
// how far its download is.
func (a *app) modelCard(c *ui.Context) {
	m := &a.captions.model
	ui.Column(c).Grow(1).Justify(ui.Center).Gap(14).Padding(0, 12, 48, 12).MaxWidth(440).Children(func() {
		ui.Text(c, T("Captions for Podcasts")).FontSize(24).FontWeight(800).LetterSpacing(-0.3)
		ui.Text(c, Tf("Download the speech recognition model, about %s, and episodes are written down on this device as they play, scrolling like lyrics. Click a line to go there. The audio is never uploaded.", megabytes(a.captionModel().Size()))).
			FontSize(14).LineHeight(1.6).TextColor(white.Alpha(0.6))
		if m.downloading {
			share := float32(0)
			if m.total > 0 {
				share = float32(m.done) / float32(m.total)
			}
			ui.Box(c).Height(6).Radius(3).Background(white.Alpha(0.14)).Margin(6, 0, 0, 0).Children(func() {
				ui.Box(c).Height(6).Radius(3).WidthPercent(share * 100).Background(white.Alpha(0.85))
			})
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, Tf("Downloading %s / %s", megabytes(m.done), megabytes(m.total))).FontSize(12.5).FontFeatures("tnum").TextColor(white.Alpha(0.5))
				ui.Spacer(c)
				if pillButton(c.Key("cancel"), nil, T("Cancel"), white.Alpha(0.14), white).Clicked() {
					a.cancelModelDownload()
				}
			})
			return
		}
		if m.err != "" {
			ui.Text(c, m.err).FontSize(13).TextColor(ui.Hex("#ff8a80"))
		}
		label := T("Download Model")
		if m.err != "" {
			label = T("Retry")
		}
		ui.Row(c).Margin(6, 0, 0, 0).Children(func() {
			if pillButton(c.Key("download"), nil, label, white, ui.Hex("#141416")).Clicked() {
				a.startModelDownload()
			}
		})
	})
}

// megabytes says n bytes in megabytes, or gigabytes once there are many.
func megabytes(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", n>>20)
}

// captionSettings are the settings' rows for captions.
func (a *app) captionSettings(row func(label, desc string, control func()), c *ui.Context) {
	m := &a.captions.model
	s := &a.settings
	row(T("Podcast Captions"), T("Writes down episodes on this device as they play, into captions that scroll. Uses a little more power."), func() {
		if ui.Switch(c.Key("captions"), &s.Captions).Label(T("Podcast Captions")).Disabled(!a.modelReady()).Changed() {
			a.changed()
			a.syncCaptions()
		}
	})
	if translationAvailable() {
		label := T("Translate English Podcast Captions")
		row(label, T("Show Traditional Chinese under the English subtitles using local Qwen3-4B."), func() {
			if ui.Switch(c.Key("translate-podcast-captions"), &s.TranslateCaptions).Label(label).Changed() {
				a.resetCaptionTranslation()
				a.changed()
			}
		})
	}
	var desc string
	switch {
	case m.downloading:
		desc = Tf("Downloading %s / %s", megabytes(m.done), megabytes(m.total))
	case a.modelReady():
		desc = megabytes(a.captionModel().Size())
	default:
		desc = megabytes(a.captionModel().Size())
	}
	row(T("Speech Recognition Model"), desc, func() {
		names := []string{"Qwen3-ASR 0.6B", "Qwen3-ASR 1.7B"}
		models := []string{speech.ModelName, speech.LargeModelName}
		if speech.WhisperAvailable() {
			names = append(names, "Whisper Large v3 Turbo (zh-TW)")
			models = append(models, speech.WhisperModelName)
		}
		chosen := names[0]
		for i, model := range models {
			if string(a.captionModel()) == model {
				chosen = names[i]
			}
		}
		if ui.Select(c.Key("caption-model"), &chosen, names).
			Label(T("Speech Recognition Model")).Width(235).Disabled(m.downloading || m.deleting).Changed() {
			for i, name := range names {
				if chosen == name {
					a.selectCaptionModel(speech.SelectModel(models[i]))
					break
				}
			}
			return
		}

		switch {
		case m.deleting:
			return
		case m.downloading:
			if ui.Button(c.Key("model"), T("Cancel")).Clicked() {
				a.cancelModelDownload()
			}
		case a.modelReady():
			if ui.Button(c.Key("model"), T("Delete")).Clicked() {
				a.deleteModel()
			}
		default:
			if ui.Button(c.Key("model"), T("Download")).Clicked() {
				a.startModelDownload()
			}
		}
	})
}
