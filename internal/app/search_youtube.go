package app

import (
	"net/url"
	"regexp"
	"strings"

	"mygo-music/internal/music"
)

var youtubeVideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// A bare ID might also be an ordinary search term. Links and v= are explicit.
func youtubeSearchID(input string) (id string, explicit bool) {
	if youtubeVideoID.MatchString(input) {
		return input, false
	}
	if query := strings.TrimPrefix(input, "?"); strings.HasPrefix(query, "v=") {
		values, _ := url.ParseQuery(query)
		id = values.Get("v")
	} else {
		if !strings.Contains(input, "://") {
			input = "https://" + input
		}
		u, err := url.Parse(input)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
			return "", false
		}
		switch strings.ToLower(u.Hostname()) {
		case "youtu.be":
			id = strings.Trim(u.Path, "/")
		case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com":
			switch path := strings.Trim(u.Path, "/"); path {
			case "watch":
				id = u.Query().Get("v")
			default:
				kind, value, _ := strings.Cut(path, "/")
				if kind == "shorts" || kind == "live" || kind == "embed" {
					id = value
				}
			}
		}
	}
	if !youtubeVideoID.MatchString(id) {
		return "", false
	}
	return id, true
}

type youtubeTrackSource interface {
	YouTubeTrack(id string) (*music.Track, error)
}

func (a *app) youtubeSearchSource() (*library, youtubeTrackSource) {
	if a.library.can("youtubeTrack") {
		if source, ok := a.library.src.(youtubeTrackSource); ok {
			return a.library, source
		}
	}
	for _, lib := range a.libs {
		if lib.can("youtubeTrack") {
			if source, ok := lib.src.(youtubeTrackSource); ok {
				return lib, source
			}
		}
	}
	return nil, nil
}

func (a *app) playYouTubeSearch(q, id string, explicit bool) bool {
	lib, source := a.youtubeSearchSource()
	if source == nil && !explicit {
		return false
	}
	a.dismissSuggestions()
	if source == nil {
		a.toast = T("Enable the YouTube Music plugin to play this link")
		return true
	}
	submit, page, input := a.searchSubmit, a.route, a.query
	go func() {
		track, err := source.YouTubeTrack(id)
		a.post(func() {
			if a.searchSubmit != submit || a.route != page || a.query != input {
				return
			}
			if err == nil && track != nil && track.ID == id && !track.Unavailable && a.pluginEnabled(lib) {
				a.pl.PlayTrack([]music.Track{*track}, 0)
				return
			}
			if !explicit {
				a.searchKeywords(q)
			} else if err != nil {
				a.toast = a.problem(lib, err, T("Couldn't open this YouTube track"))
			} else {
				a.toast = T("Couldn't find this YouTube track")
			}
		})
	}()
	return true
}
