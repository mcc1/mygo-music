package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// Radios play on without end: a station of a source, whose StationNext goes on
// giving tracks. The player knows one by a name, which says all the app needs
// after a restart: the source, the station, and what it's called.

// radioName names a station for the player.
func radioName(src string, st *music.Station) string {
	return src + "\x00" + st.ID + "\x00" + st.Name
}

// radioParts takes a radio's name apart.
func radioParts(name string) (src, id, label string) {
	parts := strings.SplitN(name, "\x00", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

// radioLabel names a radio as the listener knows it.
func radioLabel(name string) string {
	_, _, label := radioParts(name)
	return label
}

func playable(tracks []music.Track) []music.Track {
	var out []music.Track
	for _, t := range tracks {
		if !t.Unavailable {
			out = append(out, t)
		}
	}
	return out
}

// radioFeed returns what gives the radio name more tracks: it loads them
// on a goroutine, and gives them to got on the main thread, none if it
// cannot.
func (a *app) radioFeed(name string) func(got func([]music.Track)) {
	return func(got func([]music.Track)) {
		src, id, _ := radioParts(name)
		lib := a.libOf(src)
		if lib == nil {
			got(nil)
			return
		}
		go func() {
			// A batch may hold only what the listener can't play: ask again
			// for those.
			var tracks []music.Track
			var err error
			for tries := 0; tries < 3 && len(tracks) == 0; tries++ {
				var more []music.Track
				more, err = lib.src.StationNext(id)
				if err != nil {
					break
				}
				tracks = playable(more)
			}
			a.post(func() {
				if err != nil {
					a.problem(lib, err, T("The station didn't answer"))
				}
				got(tracks)
			})
		}()
	}
}

// playStation plays a station, or goes on playing it.
func (a *app) playStation(lib *library, st *music.Station) {
	if lib.signedOut() {
		a.promptSignIn(lib)
		return
	}
	name := radioName(lib.id, st)
	if a.pl.Radio() == name {
		if !a.pl.Playing() {
			a.pl.TogglePlay()
		}
		return
	}
	a.radioStarting = name
	feed := a.radioFeed(name)
	feed(func(tracks []music.Track) {
		if a.radioStarting != name {
			return
		}
		a.radioStarting = ""
		if len(tracks) == 0 {
			a.toast = T("This station can't play right now. Try again later.")
			return
		}
		a.pl.PlayRadio(name, tracks, feed)
	})
}

// exitRadio stops the radio playing, but not the song it plays.
func (a *app) exitRadio() {
	name := a.pl.Radio()
	if name == "" {
		return
	}
	a.pl.EndRadio()
	a.toast = Tf("Left %s", radioLabel(name))
}

// loadStations loads a source's stations, once.
func (a *app) loadStations(lib *library) {
	if lib.stationsBusy || lib.stationsOnce {
		return
	}
	lib.stationsBusy, lib.stationsErr = true, ""
	post := a.accountPost(lib)
	go func() {
		stations, err := lib.src.Stations()
		post(func() {
			lib.stationsBusy = false
			if err != nil {
				lib.stationsErr = a.problem(lib, err, T("Couldn't load the stations"))
				return
			}
			lib.stations, lib.stationsOnce = stations, true
		})
	}()
}

// stationsPage is what a station of this source plays, large, over its cover's
// colors, with its controls, or how to start one, and the source's stations.
func (a *app) stationsPage(c *ui.Context) {
	lib := a.library
	if !lib.stationsOnce && !lib.stationsBusy {
		a.centered(c, lib.stationsErr, func() { a.loadStations(lib) })
		return
	}
	top, bottom := a.insets(c)
	ui.Scroll(c.Key(a.route)).TrackScroll(&a.pageScroll).Grow(1).Padding(top+pageGap, pagePad, bottom+48, pagePad).ScrollbarInsets(top, 0, bottom, 0).Children(func() {
		a.startMark(c)
		defer a.endMark(c)
		// A few stations are ways of one radio, chosen with a switch by the heading;
		// more are a grid of their own under the card.
		few := len(lib.stations) > 1 && len(lib.stations) <= maxSwitchedStations
		ui.Row(c).AlignItems(ui.End).Children(func() {
			ui.Box(c).Grow(1).Shrink(1).MinWidth(0).Children(func() { a.heading(c, a.builtinTitle("stations"), T("Music without end")) })
			if few {
				a.stationSwitch(c, lib)
			}
		})
		if !lib.stationsOnce {
			a.playlistCardsSkeleton(c, a.pageShimmer(c), a.columns(c, 156), 156, false)
			return
		}
		if len(lib.stations) == 0 {
			a.empty(c, T("No stations yet"))
			return
		}
		a.radioCard(c, lib)
		if len(lib.stations) <= maxSwitchedStations {
			return
		}
		a.section(c, T("Stations"))
		ui.Grid(c).Columns(a.columns(c, 156)).GapX(20).GapY(28).Children(func() {
			for i := range lib.stations {
				a.stationCard(c.Key(lib.stations[i].ID), &lib.stations[i])
			}
		})
	})
}

// maxSwitchedStations is how many stations fit a switch.
const maxSwitchedStations = 4

// stationSwitch chooses among a few stations. It shows the one playing, and
// playing one, choosing another changes to it at once.
func (a *app) stationSwitch(c *ui.Context, lib *library) {
	src, playing, _ := radioParts(a.pl.Radio())
	labels := make([]string, len(lib.stations))
	for i, st := range lib.stations {
		labels[i] = st.Name
		if src == lib.id && st.ID == playing {
			a.stationAt = i
		}
	}
	a.stationAt = min(a.stationAt, len(labels)-1)
	at := a.stationAt
	ui.Box(c).Margin(0, 0, 22, 0).Children(func() {
		if segmented(c.Key("station"), &at, labels...) {
			a.stationAt = at
			if src == lib.id {
				a.playStation(lib, &lib.stations[at])
			}
		}
	})
}

// stationCard is a station's cover over its name: a click plays it.
func (a *app) stationCard(c *ui.Context, st *music.Station) {
	lib := a.library
	if a.coverCardWith(c, st.Name, st.ImageURL, iconRadio, st.Description, func() { a.playStation(lib, st) }) {
		a.playStation(lib, st)
	}
}

// radioCard shows the song the station plays, or how to start the first.
func (a *app) radioCard(c *ui.Context, lib *library) {
	pl := a.pl
	src, _, _ := radioParts(pl.Radio())
	onRadio := src == lib.id
	var tr *music.Track
	if onRadio {
		tr = pl.Current()
	}
	var art *artwork
	if tr != nil {
		art = a.art.get(sized(tr.Album.ImageURL, 600), true)
	}
	card := ui.Box(c.Key("radio-card")).Height(320).Radius(24).Background(ui.Hex("#232328")).TextColor(white).
		Shadow(0, 18, 44, -16, a.pal.cardShade)
	in := artIn(card, backdrop(art))
	card.Children(func() {
		if in < 1 {
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(24).
				LinearGradient(ui.LinearGradient{Angle: 135, Oklab: true, From: ui.Oklch(0.42, 0.14, 20), To: ui.Oklch(0.3, 0.1, 300)})
		}
		artImage(c, backdrop(art), 24, in)
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(24).
			LinearGradient(ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
				{Color: ui.RGBA(0, 0, 0, 0.35), At: 0},
				{Color: ui.RGBA(0, 0, 0, 0.1), At: 1},
			}})
		ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Padding(36, 40).Gap(40).AlignItems(ui.Center).Children(func() {
			a.radioCovers(c, tr)
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Children(func() {
				first := &lib.stations[min(a.stationAt, len(lib.stations)-1)]
				label := first.Name
				if onRadio {
					label = radioLabel(pl.Radio())
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconRadio).Size(14, 14).TextColor(white.Alpha(0.75))
					ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(white.Alpha(0.75))
				})
				if tr == nil {
					ui.Text(c, T("Hear something new")).FontSize(30).FontWeight(800).LetterSpacing(-0.6)
					if first.Description != "" {
						ui.Text(c, first.Description).FontSize(13.5).TextColor(white.Alpha(0.75)).MaxLines(2)
					}
					ui.Row(c).Margin(18, 0, 0, 0).Children(func() {
						label := T("Start Listening")
						if a.radioStarting != "" {
							label = T("Tuning in…")
						}
						if pillButton(c.Key("start"), iconPlay, label, white, ui.Hex("#1d1d1f")).Clicked() {
							a.playStation(lib, first)
						}
					})
					return
				}
				ui.Text(c, tr.Name).FontSize(30).FontWeight(800).LetterSpacing(-0.6).MaxLines(2).LineHeight(1.2)
				switch {
				case pl.Loading():
					ui.Text(c, T("Buffering…")).FontSize(14).TextColor(white.Alpha(0.7)).SingleLine()
				case pl.Err() != "":
					ui.Text(c, pl.Err()).FontSize(14).TextColor(white).SingleLine()
				default:
					a.artistLinks(c, tr, white.Alpha(0.7), white, true).FontSize(14).SingleLine()
				}
				ui.Row(c).Gap(14).AlignItems(ui.Center).Margin(20, 0, 0, -8).Children(func() {
					a.likeButton(c.Key("like"), tr, 20, white.Alpha(0.8))
					play := ui.ButtonBase(c.Key("play")).Size(56, 56).Center().Radius(999).Background(white).TextColor(ui.Hex("#1d1d1f")).Label(T("Play/Pause"))
					pose := ui.Pose{}
					if play.Pressed() {
						pose.Scale = 0.92
					} else if play.Hovered() {
						pose.Scale = 1.05
					}
					play.AnimatePose(pose, ui.Snappy)
					play.Children(func() { playPause(c, play, pl.Playing(), 22) })
					if play.Clicked() {
						pl.TogglePlay()
					}
					if iconButton(c.Key("next"), iconNext, 20, white.Alpha(0.8), T("Next")).Clicked() {
						pl.Next()
					}
				})
			})
		})
	})
}

// radioCovers stacks the cover playing over the next one's, which peeks out
// behind it, tilted.
func (a *app) radioCovers(c *ui.Context, tr *music.Track) {
	const size = 232
	ui.Box(c).Size(size+36, size).Shrink(0).Children(func() {
		var next *music.Track
		if tr != nil {
			if order, at := a.pl.PlayOrder(); at >= 0 && at+1 < len(order) {
				next = &a.pl.Queue()[order[at+1]]
			}
		}
		if next != nil {
			back := ui.Box(c.Key(next.ID)).Absolute().Top(16).Left(36).Size(size-32, size-32).Radius(14).Opacity(0.55).Background(white.Alpha(0.12)).
				Shadow(0, 10, 24, -8, ui.RGBA(0, 0, 0, 0.4))
			back.AnimatePose(ui.Pose{Rotate: 6, X: 14}, ui.Animation{Spring: ui.SpringOptions{Duration: 500 * time.Millisecond, Bounce: 0.2}})
			back.Children(func() { a.coverFill(c, next.Album.ImageURL, 400, 14) })
		}
		front := ui.Box(c.Key("front")).Absolute().Top(0).Left(0).Size(size, size).Radius(16).
			Background(white.Alpha(0.12)).Shadow(0, 20, 44, -10, ui.RGBA(0, 0, 0, 0.55))
		if tr == nil {
			front.Center().Children(func() {
				ui.Icon(c, iconRadio).Size(64, 64).TextColor(white.Alpha(0.85))
			})
			return
		}
		scale := float32(1)
		if !a.pl.Playing() {
			scale = 0.94
		}
		front.AnimatePose(ui.Pose{Scale: scale}, ui.Animation{Spring: ui.SpringOptions{Duration: 600 * time.Millisecond, Bounce: 0.25}})
		front.Children(func() {
			ui.Box(c.Key(tr.ID)).Fill().Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.92}}).
				Children(func() { a.coverFill(c, tr.Album.ImageURL, 600, 16) })
		})
	})
}
