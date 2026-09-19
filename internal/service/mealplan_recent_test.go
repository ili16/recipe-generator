package service

import (
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// The window is the whole point of 11.2: a dish eaten 13 days ago is still "lately" and
// one eaten 15 days ago is fair game again, and the list has to come back newest first.
func TestRecentlyCooked(t *testing.T) {
	now := time.Date(2026, 9, 19, 18, 0, 0, 0, time.UTC)
	at := func(days int) *time.Time { d := now.AddDate(0, 0, -days); return &d }
	rating := int16(4)

	recipes := []model.Recipe{
		{ID: 1, Recipename: "chilli", LastCookedAt: at(13)},
		{ID: 2, Recipename: "never cooked"},
		{ID: 3, Recipename: "old curry", LastCookedAt: at(15)},
		{ID: 4, Recipename: "last night", LastCookedAt: at(1), CookedRating: &rating},
	}

	got := recentlyCooked(recipes, now, recentCookWindowDays)
	if len(got) != 2 {
		t.Fatalf("want the two cooked inside the window, got %+v", got)
	}
	if got[0].ID != 4 || got[0].DaysAgo != 1 || got[0].Rating == nil {
		t.Errorf("newest first, with its rating: got %+v", got[0])
	}
	if got[1].ID != 1 || got[1].CookedOn != "2026-09-06" {
		t.Errorf("13 days ago is still inside the window: got %+v", got[1])
	}
	if len(recentlyCooked(recipes, now, 0)) != 0 {
		t.Error("a zero-day window is only today")
	}
}
