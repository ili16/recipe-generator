package service

import (
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestMatchRankReadsPastTheTitle pins BACKLOG 18.4's cheap half: the library search used
// to be strings.Contains on the title alone, so "the one with the miso" found nothing
// even though the document says miso in step two. Body hits count, but rank below a name
// or a tag so they never outrank what the user actually asked for.
func TestMatchRankReadsPastTheTitle(t *testing.T) {
	soup := model.Recipe{
		Recipename: "Weeknight Ramen",
		Tags:       []string{"quick", "soup"},
		Recipe:     "# Weeknight Ramen\n\n- 2 tbsp miso paste\n\n1. Whisk the miso into the stock.",
	}

	for _, c := range []struct {
		query string
		want  int
	}{
		{"", 2},         // no query: everything passes, as before
		{"ramen", 2},    // title
		{"SOUP", 2},     // tag, case-insensitive
		{"miso", 1},     // body only — this is the case that used to return nothing
		{"pancakes", 0}, // absent everywhere
	} {
		if got := matchRank(soup, c.query); got != c.want {
			t.Errorf("matchRank(%q) = %d, want %d", c.query, got, c.want)
		}
	}

	// A title hit must outrank a body hit, or the ordering is decoration.
	other := model.Recipe{Recipename: "Miso Butter Cod", Recipe: "Roast it."}
	if matchRank(other, "miso") <= matchRank(soup, "miso") {
		t.Error("a recipe named for the query did not outrank one that only mentions it")
	}
}
