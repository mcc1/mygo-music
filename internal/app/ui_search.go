package app

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/i18n"
	"mygo-music/internal/music"
)

// searchKinds are the kinds of search, as their tabs show them, and what the
// source has to have for the kind's results to go anywhere.
var searchKinds = []struct {
	kind  music.SearchKind
	label string
	needs string
}{
	{music.SearchTracks, N("Songs"), ""},
	{music.SearchArtists, N("Artists"), "artist"},
	{music.SearchAlbums, N("Albums"), "album"},
	{music.SearchPlaylists, N("Playlists"), "playlist"},
	{music.SearchShows, N("Podcasts"), "show"},
}

// searchData is what a search found, as far as its pages have loaded.
type searchData struct {
	res  music.SearchResult
	page paged
}

// search searches for q, of the kind last searched for.
func (a *app) search(q string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return
	}
	a.searchSubmit++
	if id, explicit := youtubeSearchID(q); id != "" && a.playYouTubeSearch(q, id, explicit) {
		return
	}
	a.searchKeywords(q)
}

func (a *app) searchKeywords(q string) {
	kind := a.searched.kind
	if kind == "" {
		kind = music.SearchTracks
	}
	a.searchFor(route{page: pageSearch, src: a.id, query: q, kind: kind})
}

// searchFor opens the page of a search, remembering its query. Searched
// for again, it loads anew.
func (a *app) searchFor(r route) {
	a.rememberSearch(r.query)
	a.dismissSuggestions()
	if r == a.route {
		a.nowPlaying = false
		a.loadSearch(r, true)
		return
	}
	a.open(r)
}

// loadSearch loads the first page of a search, unless it has, or again.
func (a *app) loadSearch(r route, again bool) {
	lib := a.libOf(r.src)
	if lib == nil || lib.searches[r] != nil && !again {
		return
	}
	d := &searchData{}
	lib.searches[r] = d
	a.moreResults(lib, r, d)
}

// moreResults loads the next page of a search. A search done again has
// new data, so the last one's pages land where nothing shows them.
func (a *app) moreResults(lib *library, r route, d *searchData) {
	if d.page.loading {
		return
	}
	d.page.loading, d.page.err = true, ""
	offset := d.res.Len()
	go func() {
		res, err := answered(lib.src.Search(r.query, r.kind, offset))
		a.post(func() {
			d.page.loading = false
			if err != nil {
				d.page.err = a.problem(lib, err, T("The search failed"))
				return
			}
			d.res.Tracks = append(d.res.Tracks, res.Tracks...)
			d.res.Albums = append(d.res.Albums, res.Albums...)
			d.res.Artists = append(d.res.Artists, res.Artists...)
			d.res.Playlists = append(d.res.Playlists, res.Playlists...)
			d.res.Shows = append(d.res.Shows, res.Shows...)
			// A server can claim more while sending none.
			d.page.loaded, d.page.more = true, res.More && res.Len() > 0
		})
	}()
}

// searchHistoryLimit is how many searches the history keeps.
const searchHistoryLimit = 20

// rememberSearch puts q first in the history of searches, once.
func (a *app) rememberSearch(q string) {
	history := []string{q}
	for _, s := range a.settings.SearchHistory {
		if s != q && len(history) < searchHistoryLimit {
			history = append(history, s)
		}
	}
	a.settings.SearchHistory = history
	a.settings.save()
}

func (a *app) clearSearchHistory() {
	a.settings.SearchHistory = nil
	a.settings.save()
}

func (a *app) searchPage(c *ui.Context) {
	r := a.route
	lib := a.library
	d := lib.searches[r]
	if d == nil {
		a.loadSearch(r, false)
		d = lib.searches[r]
	}
	header := func(c *ui.Context) { a.searchHeader(c, d) }
	more := func() { a.moreResults(lib, r, d) }
	if r.kind == music.SearchTracks {
		if !d.page.loaded && d.page.loading {
			a.trackSkeleton(c, a.pageShimmer(c), true, header)
			return
		}
		a.trackRows(c, d.res.Tracks, true, header, func(c *ui.Context) {
			a.pageFoot(c, &d.page, more)
			a.searchEmpty(c, d)
		})
		return
	}
	a.scrollPage(c, func() {
		header(c)
		res := &d.res
		switch r.kind {
		case music.SearchArtists:
			ui.Grid(c).Columns(a.columns(c, 132)).GapX(20).GapY(28).Children(func() {
				for i := range res.Artists {
					a.artistCard(c.Key(res.Artists[i].ID), &res.Artists[i])
				}
			})
		case music.SearchAlbums:
			a.albumGrid(c, res.Albums)
		case music.SearchPlaylists:
			ui.Grid(c).Columns(a.columns(c, 156)).GapX(20).GapY(28).Children(func() {
				for i := range res.Playlists {
					a.playlistCard(c.Key(res.Playlists[i].ID), &res.Playlists[i])
				}
			})
		case music.SearchShows:
			for i := range res.Shows {
				a.showRow(c.Key(res.Shows[i].ID), &res.Shows[i])
			}
		}
		if !d.page.loaded && d.page.loading {
			a.playlistCardsSkeleton(c, a.pageShimmer(c), a.columns(c, 156), 156, false)
			return
		}
		a.pageFoot(c, &d.page, more)
		a.searchEmpty(c, d)
	})
}

func (a *app) searchEmpty(c *ui.Context, d *searchData) {
	if d.page.loaded && d.res.Len() == 0 {
		a.empty(c, T("Nothing found"))
	}
}

// searchHeader is what was searched for, and the tabs of what it may look for
// instead, as far as the source has their pages.
func (a *app) searchHeader(c *ui.Context, d *searchData) {
	r := a.route
	sub := T("Search")
	if d.page.loaded && r.kind == music.SearchTracks {
		sub = Tf("Search · %s", i18n.Songs(len(d.res.Tracks)))
	}
	a.heading(c, "“"+r.query+"”", sub)
	var kinds []music.SearchKind
	var labels []string
	at := 0
	for _, k := range searchKinds {
		if k.needs == "" || a.can(k.needs) {
			if k.kind == r.kind {
				at = len(kinds)
			}
			kinds = append(kinds, k.kind)
			labels = append(labels, T(k.label))
		}
	}
	if len(kinds) < 2 {
		return
	}
	ui.Row(c).Margin(0, 0, 18, 0).Children(func() {
		if segmented(c.Key("kind"), &at, labels...) {
			r.kind = kinds[at]
			a.show(r)
		}
	})
}

// artistCard is an artist's round photo over their name, opening their page.
func (a *app) artistCard(c *ui.Context, ar *music.Artist) {
	p := a.pal
	card := ui.ButtonBase(c).Column().Gap(10).AlignItems(ui.Stretch).FocusRing(false).Label(ar.Name)
	hovered := card.Hovered()
	card.Children(func() {
		art := ui.Box(c).AspectRatio(1).Radius(999)
		if hovered {
			art.Shadow(0, 12, 28, -6, p.cardShade)
		} else {
			art.Shadow(0, 2, 8, -2, p.cardShade)
		}
		art.Children(func() { a.coverFill(c, ar.ImageURL, 400, 999) })
		ui.Text(c, ar.Name).FontSize(13).FontWeight(500).SingleLine().AlignSelf(ui.Center)
	})
	if card.Clicked() && a.can("artist") {
		a.open(route{page: pageArtist, id: ar.ID})
	}
}
