// Package i18n is the app's languages. The English text is the key: T returns
// it as it is in English, and its translation in any other language. Keys are
// literal strings at the call, for a test to find them all (see catalog_test).
package i18n

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// System is the setting of following the system's language: see Resolve.
	System             = "system"
	English            = "en"
	Chinese            = "zh-CN"
	TraditionalChinese = "zh-TW"
)

type locale struct {
	Code, Name string
	catalog    map[string]string
	dateLayout string
	today      func(time.Time, string) string
	countUnits []countUnit
}

type countUnit struct {
	minimum, divisor int
	suffix           string
}

var english = locale{
	Code: English, Name: "English",
	dateLayout: "January 2",
	countUnits: []countUnit{{1e9, 1e9, "B"}, {1e6, 1e6, "M"}, {1e4, 1e3, "K"}},
}

// Languages lists the available locales in the order shown in settings.
var Languages = []*locale{
	&english,
	{
		Code: Chinese, Name: "简体中文",
		catalog: zhCN, dateLayout: "1月2日", today: chineseToday,
		countUnits: []countUnit{{1e8, 1e8, "亿"}, {1e4, 1e4, "万"}},
	},
	{
		Code: TraditionalChinese, Name: "繁體中文",
		catalog: zhTW, dateLayout: "1月2日", today: chineseToday,
		countUnits: []countUnit{{1e8, 1e8, "億"}, {1e4, 1e4, "萬"}},
	},
}

func lookup(code string) *locale {
	for _, l := range Languages {
		if strings.EqualFold(l.Code, code) {
			return l
		}
	}
	return nil
}

// Resolve is the language to speak for a setting: the one chosen, or for System,
// the first supported system preference, or English when none matches.
func Resolve(setting string, preferred []string) string {
	if l := lookup(setting); l != nil {
		return l.Code
	}
	for _, tag := range preferred {
		tag = strings.ToLower(strings.ReplaceAll(tag, "_", "-"))
		if l := lookup(tag); l != nil {
			return l.Code
		}
		base, subtags, _ := strings.Cut(tag, "-")
		if base == "zh" {
			first, _, _ := strings.Cut(subtags, "-")
			switch first {
			case "hant", "tw", "hk", "mo":
				return TraditionalChinese
			}
			return Chinese
		}
		if l := lookup(base); l != nil {
			return l.Code
		}
	}
	return English
}

var language atomic.Pointer[locale]

// SetLanguage switches the language of what T returns from now on. A language
// the app doesn't have is English.
func SetLanguage(code string) {
	language.Store(lookup(code))
}

// Language returns the language T translates to.
func Language() string { return currentLocale().Code }

func currentLocale() *locale {
	if l := language.Load(); l != nil {
		return l
	}
	return &english
}

// T returns the text in the listener's language.
func T(text string) string {
	if translated, ok := currentLocale().catalog[text]; ok {
		return translated
	}
	return text
}

// TC is T of a text that reads differently by what it's about, as Save is one
// word for keeping a name and another for adding an album to the library. The
// catalog has it as "text|context", and English reads the text.
func TC(text, context string) string {
	if translated, ok := currentLocale().catalog[text+"|"+context]; ok {
		return translated
	}
	return text
}

// Tf is T of a format, formatted. Its arguments must stay in the order the
// English has them, or the translation numbers them, as %[2]d.
func Tf(format string, args ...any) string {
	return fmt.Sprintf(T(format), args...)
}

// N marks text for translation where it's kept to show later, through T, as in
// a table: it returns it as it is.
func N(text string) string { return text }

// Ago says how long before now t was.
func Ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return T("just now")
	case d < 2*time.Minute:
		return T("a minute ago")
	case d < time.Hour:
		return Tf("%d minutes ago", int(d/time.Minute))
	case d < 2*time.Hour:
		return T("an hour ago")
	case d < 24*time.Hour:
		return Tf("%d hours ago", int(d/time.Hour))
	case d < 48*time.Hour:
		return T("yesterday")
	case d < 30*24*time.Hour:
		return Tf("%d days ago", int(d/(24*time.Hour)))
	}
	if t.Year() == now.Year() {
		return Date(t)
	}
	return t.Format("2006-01-02")
}

// Date says t's month and day, and day of the week in Today.
func Date(t time.Time) string {
	return t.Format(currentLocale().dateLayout)
}

// Today says the day of t, as a page heading does.
func Today(t time.Time) string {
	l := currentLocale()
	date := t.Format(l.dateLayout)
	if l.today != nil {
		return l.today(t, date)
	}
	return t.Weekday().String() + ", " + date
}

func chineseToday(t time.Time, date string) string {
	weekdays := [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	return date + " " + weekdays[t.Weekday()]
}

// Songs says how many songs there are.
func Songs(n int) string {
	if n == 1 {
		return T("1 song")
	}
	return Tf("%d songs", n)
}

// Count says a count shortly, as people read it in the language.
func Count(n int) string {
	for _, unit := range currentLocale().countUnits {
		if n >= unit.minimum {
			return fmt.Sprintf("%.1f%s", float64(n)/float64(unit.divisor), unit.suffix)
		}
	}
	return fmt.Sprint(n)
}
