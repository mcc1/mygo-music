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

// Languages are those the listener can choose, each named in itself.
var Languages = []struct{ Code, Name string }{
	{English, "English"},
	{Chinese, "简体中文"},
	{TraditionalChinese, "繁體中文"},
}

// Resolve is the language to speak for a setting: the one chosen, or for System,
// the first supported system preference, or English when none matches.
func Resolve(setting string, preferred []string) string {
	switch setting {
	case English, Chinese, TraditionalChinese:
		return setting
	}
	for _, tag := range preferred {
		base, subtags, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(tag, "_", "-")), "-")
		switch base {
		case "zh":
			first, _, _ := strings.Cut(subtags, "-")
			switch first {
			case "hant", "tw", "hk", "mo":
				return TraditionalChinese
			}
			return Chinese
		case "en":
			return English
		}
	}
	return English
}

var language atomic.Pointer[string]

// SetLanguage switches the language of what T returns from now on. A language
// the app doesn't have is English.
func SetLanguage(code string) {
	if code != Chinese && code != TraditionalChinese {
		code = English
	}
	language.Store(&code)
}

// Language returns the language T translates to.
func Language() string {
	if l := language.Load(); l != nil {
		return *l
	}
	return English
}

// T returns the text in the listener's language.
func T(text string) string {
	if translated, ok := catalog()[text]; ok {
		return translated
	}
	return text
}

// TC is T of a text that reads differently by what it's about, as Save is one
// word for keeping a name and another for adding an album to the library. The
// catalog has it as "text|context", and English reads the text.
func TC(text, context string) string {
	if translated, ok := catalog()[text+"|"+context]; ok {
		return translated
	}
	return text
}

func catalog() map[string]string {
	switch Language() {
	case Chinese:
		return zhCN
	case TraditionalChinese:
		return zhTW
	default:
		return nil
	}
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
	if lang := Language(); lang == Chinese || lang == TraditionalChinese {
		return fmt.Sprintf("%d月%d日", t.Month(), t.Day())
	}
	return fmt.Sprintf("%s %d", t.Month(), t.Day())
}

// Today says the day of t, as a page heading does.
func Today(t time.Time) string {
	if lang := Language(); lang == Chinese || lang == TraditionalChinese {
		return Date(t) + " " + [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[t.Weekday()]
	}
	return t.Weekday().String() + ", " + Date(t)
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
	lang := Language()
	if lang == Chinese || lang == TraditionalChinese {
		hundredMillion, tenThousand := "亿", "万"
		if lang == TraditionalChinese {
			hundredMillion, tenThousand = "億", "萬"
		}
		switch {
		case n >= 1e8:
			return fmt.Sprintf("%.1f%s", float64(n)/1e8, hundredMillion)
		case n >= 1e4:
			return fmt.Sprintf("%.1f%s", float64(n)/1e4, tenThousand)
		}
		return fmt.Sprint(n)
	}
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e4:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
