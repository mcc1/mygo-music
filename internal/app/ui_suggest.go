package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// suggestDelay is how long typing pauses before suggestions are asked for.
const suggestDelay = 220 * time.Millisecond

// suggestState is what a search may be after, as its query is typed.
type suggestState struct {
	query   string   // what they are for
	items   []string // what the source says it may be after
	loading bool
	failed  bool
	timer   *time.Timer // waiting for typing to pause
}

// suggest asks the source for suggestions for q, once typing pauses, if it has
// any to give.
func (a *app) suggest(q string) {
	s := &a.suggestions
	if q == s.query {
		return
	}
	a.dismissSuggestions()
	s.query = q
	lib := a.library
	if !lib.can("suggest") {
		return
	}
	s.loading = true
	s.timer = time.AfterFunc(suggestDelay, func() {
		items, err := lib.src.Suggest(q)
		a.post(func() {
			// Typed on, or searched since.
			if q != s.query {
				return
			}
			s.timer, s.loading, s.failed = nil, false, err != nil
			s.items = items[:min(len(items), 10)]
		})
	})
}

func (a *app) dismissSuggestions() {
	s := &a.suggestions
	if s.timer != nil {
		s.timer.Stop()
	}
	*s = suggestState{}
}

// searchField is the search field atop the sidebar, which suggests what
// its query may be after as it is typed, and, empty, lists the searches
// before.
func (a *app) searchField(c *ui.Context) {
	p := a.pal
	// The input fills the field, under its icon, so a click anywhere on the
	// field focuses it.
	field := ui.Row(c).Height(32).Radius(9).AlignItems(ui.Stretch).Margin(0, 0, 14, 0)
	if c.Focused(a.searchBox) {
		field.Background(p.bg).Shadow(0, 0, 0, 3, p.accent.Alpha(0.25)).Border(1, p.accent.Alpha(0.6))
	} else {
		field.Background(p.hover).Border(1, p.hairline)
	}
	field.Children(func() {
		// With something to clear, it ends in a button that clears it, as
		// NSSearchField's does: the query, and the results the sidebar
		// goes back to.
		clearable := a.query != "" || a.searched.query != "" && a.searched.src == a.id
		end := float32(10)
		if clearable {
			end = 28
		}
		cb := ui.ComboboxBase(c.Key("search"), &a.query)
		in := cb.Input.Bind(&a.searchBox).Grow(1).MinWidth(0).PaddingStart(31).PaddingEnd(end).
			Placeholder(T("Search")).FontSize(13).Label(T("Search"))
		ui.Icon(c, iconSearch).Size(14, 14).TextColor(p.muted).Absolute().Left(10).Top(8).IgnoreInput(true)
		if clearable {
			x := ui.ButtonBase(c.Key("clear-search")).Size(22, 22).Center().Absolute().Right(4).Top(4).
				KeepFocus().FocusRing(false).Label(T("Clear Search"))
			fill := p.faint
			if x.Hovered() {
				fill = p.muted
			}
			x.Children(func() {
				ui.Box(c).Size(14, 14).Radius(7).Center().Background(fill).Children(func() {
					ui.Icon(c, iconX).Size(8, 8).TextColor(p.bg)
				})
			})
			if x.Clicked() {
				cb.SetOpen(false)
				a.clearSearch()
			}
		}
		q := strings.TrimSpace(a.query)
		_, youtubeLink := youtubeSearchID(q)
		s := &a.suggestions
		switch {
		case in.Focused() && cb.Open() && q != "" && !youtubeLink:
			a.suggest(q)
		case s.query != "":
			a.dismissSuggestions()
		}
		// Empty, it lists the searches before, if any.
		if q == "" && len(a.settings.SearchHistory) == 0 {
			cb.SetOpen(false)
		}
		// It drops down as NSSearchField's menu does, in the colors of the
		// sidebar's other dropdowns: a header, the options, and clearing
		// the searches before last.
		pop := popoverColors(p.dark)
		cb.Popup(func(panel ui.Element) {
			panel.AttachTo(field, ui.AnchorBottomStart, ui.AnchorTopStart).
				Margin(6, 0, 0, 0).Padding(5).Gap(1).MaxWidth(sidebarWidth+60).
				Radius(12).Background(pop.fill).Border(0.5, pop.edge).
				Shadow(0, 8, 24, -4, ui.RGBA(0, 0, 0, 0.2))
			if w := field.Bounds().W; w > 0 {
				panel.MinWidth(w)
			}
			header := func(label string) {
				ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(p.faint).SingleLine().Padding(4, 8, 2, 8)
			}
			option := func(key, label string, weight int) {
				row := cb.Item(key).Height(26).PaddingX(8).Radius(6).AlignItems(ui.Center).Label(label)
				if row.Highlighted() {
					row.Background(p.press)
				}
				row.Children(func() {
					ui.Text(c, label).FontSize(13).FontWeight(weight).TextColor(p.text).SingleLine().Shrink(1).MinWidth(0)
				})
			}
			if q == "" {
				panel.Label(T("Recent Searches"))
				header(T("Recent Searches"))
				for _, h := range a.settings.SearchHistory {
					option("history:"+h, h, 400)
				}
				ui.Box(c).Height(0.5).Margin(4, 8).Background(pop.edge)
				option("clear", T("Clear Recent Searches"), 400)
				return
			}
			panel.Label(T("Suggestions"))
			// The query first, which Enter searches as typed.
			label := Tf("Search for “%s”", q)
			if youtubeLink {
				label = T("Play YouTube track")
			}
			option("query", label, 500)
			if s.query == q && len(s.items) > 0 {
				ui.Box(c).Height(0.5).Margin(4, 8).Background(pop.edge)
				for _, item := range s.items {
					option("suggest:"+item, item, 400)
				}
			}
		})
		if key, ok := cb.Chosen(); ok {
			cb.SetOpen(false)
			a.choose(key, q)
		} else if in.Submitted() && q != "" {
			cb.SetOpen(false)
			a.search(q)
		}
	})
}

// choose goes where the option chosen in the search field's popup goes.
func (a *app) choose(key, q string) {
	if key == "clear" {
		a.clearSearchHistory()
		return
	}
	if h, ok := strings.CutPrefix(key, "history:"); ok {
		a.query = h
		a.search(h)
		return
	}
	if key == "query" {
		a.search(q)
		return
	}
	if item, ok := strings.CutPrefix(key, "suggest:"); ok {
		a.query = item
		a.search(item)
	}
}
