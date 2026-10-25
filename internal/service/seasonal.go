package service

import (
	"strconv"
	"strings"
	"time"
)

// Seasonal calendar — GERMANY ONLY for now. Every response carries Region so the client and
// the model both say so.
//
// Sources (month granularity; a month counts if the season touches any part of it):
//   - vegetables, fruit: regional-saisonal.de, which mirrors the BZfE Saisonkalender (domestic
//     field-grown = fresh, cold-store = stored). bzfe.de publishes it only as a poster and an
//     app, so this is a secondary source: spot-check against the BZfE app before trusting a row.
//   - game: Verordnung über die Jagdzeiten (JagdzeitV), federal baseline, read 2026-10-25.
//     Bundesländer may shorten or shift these.

const seasonRegion = "DE"

type seasonItem struct {
	Name     L
	Category string // vegetable | fruit | game
	fresh    uint16 // bit m-1 set = fresh/open in month m
	stored   uint16 // bit m-1 set = available from cold storage
}

// mm parses "7-10" or "1-2,11-12" into a month bitmask; "1-12" is the whole year.
func mm(spec string) uint16 {
	var mask uint16
	for _, part := range strings.Split(spec, ",") {
		lo, hi, ok := strings.Cut(part, "-")
		a, _ := strconv.Atoi(lo)
		b := a
		if ok {
			b, _ = strconv.Atoi(hi)
		}
		if a < 1 || b > 12 || a > b {
			panic("seasonal: bad month spec " + spec)
		}
		for m := a; m <= b; m++ {
			mask |= 1 << (m - 1)
		}
	}
	return mask
}

func (i seasonItem) status(m time.Month) string {
	switch bit := uint16(1) << (m - 1); {
	case i.fresh&bit != 0:
		return "fresh"
	case i.stored&bit != 0:
		return "stored"
	}
	return ""
}

// seasonEntry is one item as the client sees it for a chosen month.
type seasonEntry struct {
	Name     L      `json:"name"`
	Category string `json:"category"`
	Status   string `json:"status"` // fresh | stored
}

type seasonResult struct {
	Region     string        `json:"region"`
	Note       string        `json:"note"`
	Month      int           `json:"month"`
	InSeason   []seasonEntry `json:"in_season"`
	EndingSoon []seasonEntry `json:"ending_soon"` // in season now, gone next month
	NextMonth  []seasonEntry `json:"next_month"`  // not in season now, in season next month
}

const seasonNote = "Germany only (domestic field-grown; game per federal hunting seasons, which Bundesländer may change). A month counts if the season touches any part of it."

func nextMonth(m time.Month) time.Month { return m%12 + 1 }

// seasonal lists what is in season in month m. category "" means all; q filters by a
// case-insensitive substring of either name.
func seasonal(m time.Month, category, q string) seasonResult {
	r := seasonResult{Region: seasonRegion, Note: seasonNote, Month: int(m),
		InSeason: []seasonEntry{}, EndingSoon: []seasonEntry{}, NextMonth: []seasonEntry{}}
	q = strings.ToLower(strings.TrimSpace(q))
	for _, it := range seasonData {
		if category != "" && it.Category != category {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Name.En), q) && !strings.Contains(strings.ToLower(it.Name.De), q) {
			continue
		}
		now, next := it.status(m), it.status(nextMonth(m))
		e := seasonEntry{it.Name, it.Category, now}
		switch {
		case now != "":
			r.InSeason = append(r.InSeason, e)
			if next == "" {
				r.EndingSoon = append(r.EndingSoon, e)
			}
		case next != "":
			e.Status = next
			r.NextMonth = append(r.NextMonth, e)
		}
	}
	return r
}

func it(en, de, cat, fresh, stored string) seasonItem {
	s := seasonItem{Name: L{en, de}, Category: cat, fresh: mm(fresh)}
	if stored != "" {
		s.stored = mm(stored)
	}
	return s
}

var seasonData = []seasonItem{
	// vegetables — fresh, then cold-store months
	it("Eggplant", "Aubergine", "vegetable", "7-10", ""),
	it("Cauliflower", "Blumenkohl", "vegetable", "5-10", ""),
	it("Green beans", "Grüne Bohnen", "vegetable", "7-10", ""),
	it("Broad beans", "Dicke Bohnen", "vegetable", "6-8", ""),
	it("Broccoli", "Brokkoli", "vegetable", "6-10", ""),
	it("Golden beetroot", "Butterrüben", "vegetable", "8-12", "1-4"),
	it("Mushrooms (button)", "Champignons", "vegetable", "1-12", ""),
	it("Peas", "Erbsen", "vegetable", "6-8", ""),
	it("Fennel", "Fenchel", "vegetable", "6-11", ""),
	it("Kale", "Grünkohl", "vegetable", "1-2,11-12", ""),
	it("Cucumber", "Gurke", "vegetable", "6-10", ""),
	it("Potatoes", "Kartoffeln", "vegetable", "6-10", "1-5,11-12"),
	it("Kohlrabi", "Kohlrabi", "vegetable", "5-10", ""),
	it("Pumpkin", "Kürbis", "vegetable", "8-11", "1-2,12"),
	it("Leek", "Lauch", "vegetable", "1-12", ""),
	it("Spring onions", "Frühlingszwiebeln", "vegetable", "5-10", ""),
	it("Sweetcorn", "Zuckermais", "vegetable", "8-10", ""),
	it("Swiss chard", "Mangold", "vegetable", "5-10", ""),
	it("Carrots", "Möhren", "vegetable", "6-10", "1-5,11-12"),
	it("Bell pepper", "Paprika", "vegetable", "7-10", ""),
	it("Parsnips", "Pastinaken", "vegetable", "1-3,9-12", "4"),
	it("Radishes", "Radieschen", "vegetable", "5-10", ""),
	it("Brussels sprouts", "Rosenkohl", "vegetable", "1-3,10-12", ""),
	it("Beetroot", "Rote Bete", "vegetable", "7-11", "1-4,12"),
	it("Red cabbage", "Rotkohl", "vegetable", "6-11", "1-5,12"),
	it("Salsify", "Schwarzwurzeln", "vegetable", "1-2,10-12", ""),
	it("Asparagus", "Spargel", "vegetable", "4-6", ""),
	it("Spinach", "Spinat", "vegetable", "3-5,9-11", ""),
	it("Pointed cabbage", "Spitzkohl", "vegetable", "5-6", ""),
	it("Celery stalks", "Staudensellerie", "vegetable", "7-10", ""),
	it("Swede", "Steckrüben", "vegetable", "9-12", "1-3"),
	it("Tomatoes", "Tomaten", "vegetable", "7-10", ""),
	it("Jerusalem artichoke", "Topinambur", "vegetable", "1-3,10-12", ""),
	it("White cabbage", "Weißkohl", "vegetable", "6-11", "1-4,12"),
	it("Savoy cabbage", "Wirsing", "vegetable", "1-2,5-12", "3"),
	it("Zucchini", "Zucchini", "vegetable", "6-10", ""),
	it("Sugar snap peas", "Zuckerschoten", "vegetable", "6-8", ""),
	it("Onions", "Zwiebeln", "vegetable", "7-10", "1-6,11-12"),
	// fruit
	it("Apple", "Apfel", "fruit", "8-11", "1-5,12"),
	it("Apricot", "Aprikose", "fruit", "7-8", ""),
	it("Pear", "Birne", "fruit", "8-10", "11-12"),
	it("Blueberries", "Heidelbeeren", "fruit", "6-9", ""),
	it("Blackberries", "Brombeeren", "fruit", "7-9", ""),
	it("Strawberries", "Erdbeeren", "fruit", "5-7", ""),
	it("Raspberries", "Himbeeren", "fruit", "6-8", ""),
	it("Elderberries", "Holunderbeeren", "fruit", "9-10", ""),
	it("Currants", "Johannisbeeren", "fruit", "6-8", ""),
	it("Cherries", "Kirschen", "fruit", "6-8", ""),
	it("Mirabelle plums", "Mirabellen", "fruit", "7-9", ""),
	it("Plums", "Pflaumen", "fruit", "7-9", ""),
	it("Quince", "Quitten", "fruit", "9-11", ""),
	it("Rhubarb", "Rhabarber", "fruit", "4-6", ""),
	it("Gooseberries", "Stachelbeeren", "fruit", "6-8", ""),
	it("Watermelon", "Wassermelone", "fruit", "8-9", ""),
	it("Grapes", "Weintrauben", "fruit", "9-10", ""),
	it("Damson plums", "Zwetschgen", "fruit", "7-10", ""),
	// game — JagdzeitV, open season (fresh = may be hunted)
	it("Red deer (stag, adult hind)", "Rotwild (Hirsch, Alttier)", "game", "8-12,1", ""),
	it("Red deer (calf)", "Rotkalb", "game", "8-12,1-2", ""),
	it("Fallow deer (stag, adult hind)", "Damwild (Hirsch, Alttier)", "game", "9-12,1", ""),
	it("Roe deer (buck)", "Rehbock", "game", "5-10", ""),
	it("Roe deer (doe)", "Ricke", "game", "9-12,1", ""),
	it("Wild boar", "Wildschwein", "game", "1-12", ""),
	it("Chamois", "Gamswild", "game", "8-12", ""),
	it("Brown hare", "Feldhase", "game", "10-12,1", ""),
	it("Pheasant", "Fasan", "game", "10-12,1", ""),
	it("Partridge", "Rebhuhn", "game", "9-12", ""),
	it("Wild duck (mallard)", "Wildente (Stockente)", "game", "9-12,1", ""),
	it("Wild goose", "Wildgans", "game", "11-12,1", ""),
	it("Wood pigeon", "Ringeltaube", "game", "11-12,1-2", ""),
	it("Woodcock", "Waldschnepfe", "game", "10-12,1", ""),
}
