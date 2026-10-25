package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func outVal(t *testing.T, o []calcOut, key string) float64 {
	t.Helper()
	for _, x := range o {
		if x.Key == key {
			return x.Value
		}
	}
	t.Fatalf("no output %q in %+v", key, o)
	return 0
}

func run(t *testing.T, id string, in map[string]any) []calcOut {
	t.Helper()
	o, _, err := runCalculator(id, in)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return o
}

func TestCalculators(t *testing.T) {
	// 4 x 250 g at 65% hydration, 2.8% salt: the parts must add back up to the dough weight.
	p := run(t, "pizza_dough", nil)
	sum := outVal(t, p, "flour") + outVal(t, p, "water") + outVal(t, p, "salt") + outVal(t, p, "yeast")
	if sum < 998 || sum > 1002 {
		t.Errorf("pizza parts add to %v, want ~1000", sum)
	}
	if w := outVal(t, p, "water") / outVal(t, p, "flour"); w < 0.645 || w > 0.655 {
		t.Errorf("hydration %v, want 0.65", w)
	}

	// 500 g flour, 75%, 20% starter at 100%: starter 100 g (50 flour + 50 water).
	s := run(t, "sourdough", nil)
	if outVal(t, s, "starter") != 100 || outVal(t, s, "add_flour") != 450 || outVal(t, s, "add_water") != 325 {
		t.Errorf("sourdough: %+v", s)
	}

	v := run(t, "vinaigrette", map[string]any{"total_ml": 100.0})
	if outVal(t, v, "oil") != 75 || outVal(t, v, "acid") != 25 {
		t.Errorf("3:1 of 100 ml: %+v", v)
	}

	// 1 kg of vegetables at the usual 2% is 20 g of salt.
	if got := outVal(t, run(t, "salt", map[string]any{"mode": "ferment"}), "salt"); got != 20 {
		t.Errorf("ferment salt = %v, want 20", got)
	}
	r := run(t, "sauce_reduce", map[string]any{"volume_ml": 1000.0, "target_ml": 250.0})
	if outVal(t, r, "boil_off") != 750 || outVal(t, r, "concentration") != 4 {
		t.Errorf("reduce: %+v", r)
	}
}

func TestCalculatorsRejectBadInput(t *testing.T) {
	for _, tc := range []struct {
		id string
		in map[string]any
	}{
		{"nope", nil},
		{"pizza_dough", map[string]any{"pizzas": 0.0}},
		{"pizza_dough", map[string]any{"pizzas": "four"}},
		{"pizza_dough", map[string]any{"where": "oven"}},
		{"sauce_reduce", map[string]any{"volume_ml": 500.0, "target_ml": 800.0}},
	} {
		if _, _, err := runCalculator(tc.id, tc.in); err == nil {
			t.Errorf("%s %v should be refused", tc.id, tc.in)
		}
	}
}

func TestSeasonal(t *testing.T) {
	for _, it := range seasonData {
		if it.fresh == 0 {
			t.Errorf("%s is never in season", it.Name.En)
		}
	}
	mar := seasonal(time.March, "vegetable", "spargel")
	if len(mar.InSeason) != 0 || len(mar.NextMonth) != 1 {
		t.Errorf("asparagus starts in April: %+v", mar)
	}
	// December wraps to January: kale is in season both months, so it is not ending.
	for _, e := range seasonal(time.December, "", "grünkohl").EndingSoon {
		t.Errorf("kale should not be ending in December: %+v", e)
	}
	if got := seasonal(time.February, "game", "wildschwein"); len(got.InSeason) != 1 || got.Region != "DE" {
		t.Errorf("wild boar is all year: %+v", got)
	}
}

func TestKitchenToolHandlers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tools", HandleListKitchenTools)
	mux.HandleFunc("GET /tools/seasonal", HandleSeasonalTool)
	mux.HandleFunc("POST /tools/{id}", HandleRunKitchenTool)
	do := func(method, url, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(body)))
		return w
	}
	if w := do("GET", "/tools", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"pizza_dough"`) {
		t.Errorf("catalog: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/tools/salt", `{"inputs":{"mode":"wet_brine","amount":2000}}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"value":120`) {
		t.Errorf("2 l at 6%% is 120 g: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/tools/pizza_dough", `{"inputs":{"pizzas":0}}`); w.Code != 400 {
		t.Errorf("out of range should be 400, got %d", w.Code)
	}
	if w := do("GET", "/tools/seasonal?month=13", ""); w.Code != 400 {
		t.Errorf("month 13 should be 400, got %d", w.Code)
	}
	if w := do("GET", "/tools/seasonal?month=3&category=vegetable", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"region":"DE"`) {
		t.Errorf("seasonal: %d %s", w.Code, w.Body)
	}
}
