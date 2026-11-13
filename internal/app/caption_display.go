package app

import (
	"log"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/longbridgeapp/opencc"

	"mygo-music/internal/i18n"
	"mygo-music/internal/lyrics"
)

var captionOpenCC = sync.OnceValue(func() *opencc.OpenCC {
	cc, err := opencc.New("s2tw")
	if err != nil {
		log.Printf("initialize Podcast caption conversion: %v", err)
	}
	return cc
})

type captionDisplay struct {
	program string
	text    map[string]string
	source  []lyrics.Line
	result  []lyrics.Line
}

func (d *captionDisplay) lines(program string, source []lyrics.Line, language string) []lyrics.Line {
	if language != i18n.TraditionalChinese || len(source) == 0 {
		return source
	}
	if d.program != program || d.text == nil {
		d.program = program
		d.text = make(map[string]string)
		d.source, d.result = nil, nil
	}
	// Caption updates replace the source slice.
	if len(source) == len(d.source) && &source[0] == &d.source[0] {
		return d.result
	}
	var converted []lyrics.Line
	for i, line := range source {
		text, cached := d.text[line.Text]
		if !cached {
			text = traditionalCaption(line.Text)
			d.text[line.Text] = text
		}
		if text != line.Text {
			if converted == nil {
				converted = slices.Clone(source)
			}
			converted[i].Text = text
			// Word offsets belong to the original text.
			converted[i].Words = nil
		}
	}
	if converted == nil {
		converted = source
	}
	d.source, d.result = source, converted
	return converted
}

func traditionalCaption(text string) string {
	if strings.ContainsFunc(text, func(r rune) bool {
		return unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
	}) || !strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		return text
	}
	if cc := captionOpenCC(); cc != nil {
		if converted, err := cc.Convert(text); err == nil {
			return converted
		}
	}
	return text
}
