package app

import (
	"context"
	"reflect"
	"testing"
)

func TestPodcastTranslationRecovery(t *testing.T) {
	batch := []translationLine{{Text: "first"}, {Text: "broken"}, {Text: "third"}, {Text: "fourth"}}
	got, err := recoverTranslations(context.Background(), batch, func(_ context.Context, lines []translationLine) ([]string, error) {
		if len(lines) == 4 {
			return parseTranslationBatch(`[{"id":3,"text":"第三句"},{"id":1,"text":"第一句"},{"id":2,"text":"重複"},{"id":2,"text":"重複"},{"id":9,"text":"多餘"}]`, 4), nil
		}
		if len(lines) == 2 {
			return parseTranslationBatch("invalid JSON", 2), nil
		}
		if lines[0].Text == "fourth" {
			return parseTranslationBatch(`[{"id":1,"text":"第四句"}]`, 1), nil
		}
		return parseTranslationBatch("invalid JSON", 1), nil
	})
	if err != nil || !reflect.DeepEqual(got, []string{"第一句", "", "第三句", "第四句"}) {
		t.Fatalf("got %q, %v", got, err)
	}
}
