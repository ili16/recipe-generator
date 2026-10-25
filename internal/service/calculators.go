package service

import (
	"fmt"
	"math"
	"strings"
)

// Kitchen calculators: pure maths the model must not do in its head (BACKLOG Phase 10 tools).
// One implementation serves both the agent (`calculate` tool) and the Tools screen
// (GET /tools, POST /tools/{id}); the screen renders its forms from calcCatalog, so adding a
// calculator is a change in this file only.

// L is a string in the two shipped languages (BACKLOG 14).
type L struct {
	En string `json:"en"`
	De string `json:"de"`
}

type calcField struct {
	Name    string   `json:"name"`
	Label   L        `json:"label"`
	Kind    string   `json:"kind"` // number | select
	Unit    string   `json:"unit,omitempty"`
	Default any      `json:"default"`
	Min     float64  `json:"min,omitempty"`
	Max     float64  `json:"max,omitempty"`
	Options []calcOp `json:"options,omitempty"`
}

type calcOp struct {
	Value string `json:"value"`
	Label L      `json:"label"`
}

type calcOut struct {
	Key   string  `json:"key"`
	Label L       `json:"label"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
	Note  *L      `json:"note,omitempty"`
}

type calculator struct {
	ID     string      `json:"id"`
	Title  L           `json:"title"`
	Desc   L           `json:"desc"`
	Fields []calcField `json:"fields"`
	run    func(v calcVals) ([]calcOut, error)
}

// calcVals are the validated inputs, defaults filled in.
type calcVals map[string]any

func (v calcVals) n(k string) float64 { return v[k].(float64) }
func (v calcVals) s(k string) string  { return v[k].(string) }

func num(name string, label L, unit string, def, min, max float64) calcField {
	return calcField{Name: name, Label: label, Kind: "number", Unit: unit, Default: def, Min: min, Max: max}
}

func pick(name string, label L, def string, opts ...calcOp) calcField {
	return calcField{Name: name, Label: label, Kind: "select", Default: def, Options: opts}
}

func op(v, en, de string) calcOp { return calcOp{v, L{en, de}} }

func out(key, en, de string, v float64, unit string) calcOut {
	return calcOut{Key: key, Label: L{en, de}, Value: round(v, unit), Unit: unit}
}

// round: whole grams/ml, tenths for small amounts and percentages.
func round(v float64, unit string) float64 {
	if (unit == "g" || unit == "ml") && v >= 20 {
		return math.Round(v)
	}
	return math.Round(v*10) / 10
}

// resolve fills defaults and rejects out-of-range or unknown-option inputs.
func (c *calculator) resolve(in map[string]any) (calcVals, error) {
	v := calcVals{}
	for _, f := range c.Fields {
		raw, ok := in[f.Name]
		if !ok || raw == nil {
			v[f.Name] = f.Default
			continue
		}
		switch f.Kind {
		case "select":
			s, _ := raw.(string)
			valid := false
			for _, o := range f.Options {
				valid = valid || o.Value == s
			}
			if !valid {
				return nil, fmt.Errorf("%s: pick one of the listed options", f.Name)
			}
			v[f.Name] = s
		default:
			var x float64
			switch t := raw.(type) {
			case float64:
				x = t
			case int:
				x = float64(t)
			default:
				return nil, fmt.Errorf("%s must be a number", f.Name)
			}
			if math.IsNaN(x) || x < f.Min || x > f.Max {
				return nil, fmt.Errorf("%s must be between %v and %v", f.Name, f.Min, f.Max)
			}
			v[f.Name] = x
		}
	}
	return v, nil
}

// runCalculator validates the inputs and runs one calculator. Errors are ours to show.
func runCalculator(id string, in map[string]any) ([]calcOut, calcVals, error) {
	for i := range calcCatalog {
		c := &calcCatalog[i]
		if c.ID != id {
			continue
		}
		v, err := c.resolve(in)
		if err != nil {
			return nil, nil, err
		}
		o, err := c.run(v)
		return o, v, err
	}
	return nil, nil, fmt.Errorf("unknown calculator %q; one of: %s", id, strings.Join(calcIDs(), ", "))
}

func calcIDs() []string {
	ids := make([]string, len(calcCatalog))
	for i, c := range calcCatalog {
		ids[i] = c.ID
	}
	return ids
}

var calcCatalog = []calculator{
	{
		ID: "pizza_dough", Title: L{"Pizza dough", "Pizzateig"},
		Desc: L{"Flour, water, salt and yeast for a given number of pizzas.", "Mehl, Wasser, Salz und Hefe für eine bestimmte Anzahl Pizzen."},
		Fields: []calcField{
			num("pizzas", L{"Pizzas", "Pizzen"}, "", 4, 1, 50),
			num("ball_g", L{"Dough ball", "Teigling"}, "g", 250, 100, 600),
			num("hydration", L{"Hydration", "Hydration"}, "%", 65, 50, 85),
			num("salt", L{"Salt", "Salz"}, "%", 2.8, 1, 4),
			num("hours", L{"Rise time", "Gehzeit"}, "h", 24, 2, 96),
			pick("where", L{"Rises", "Gehen"}, "fridge", op("room", "at room temperature", "bei Raumtemperatur"), op("fridge", "in the fridge", "im Kühlschrank")),
		},
		run: func(v calcVals) ([]calcOut, error) {
			// ponytail: yeast % is a rough fit of common recipes (about 0.1% instant yeast at 24 h
			// room temperature); the fridge counts as a third of the time. Replace with a table if
			// bakers complain.
			eff := v.n("hours")
			if v.s("where") == "fridge" {
				eff /= 3
			}
			yeast := 1.6 / math.Pow(eff, 0.9) / 100
			h, s := v.n("hydration")/100, v.n("salt")/100
			total := v.n("pizzas") * v.n("ball_g")
			flour := total / (1 + h + s + yeast)
			return []calcOut{
				out("flour", "Flour", "Mehl", flour, "g"),
				out("water", "Water", "Wasser", flour*h, "g"),
				out("salt", "Salt", "Salz", flour*s, "g"),
				out("yeast", "Instant dry yeast", "Trockenhefe", flour*yeast, "g"),
				out("total", "Dough in total", "Teig gesamt", total, "g"),
			}, nil
		},
	},
	{
		ID: "sourdough", Title: L{"Sourdough hydration", "Sauerteig-Hydration"},
		Desc: L{"Splits the starter into its flour and water so the dough's real hydration is what you aim for.", "Teilt den Anstellgut in Mehl und Wasser, damit die echte Hydration stimmt."},
		Fields: []calcField{
			num("flour", L{"Total flour (incl. starter's)", "Gesamtmehl (inkl. Anstellgut)"}, "g", 500, 50, 5000),
			num("hydration", L{"Target hydration", "Ziel-Hydration"}, "%", 75, 55, 100),
			num("levain", L{"Starter, % of flour", "Anstellgut, % vom Mehl"}, "%", 20, 1, 50),
			num("starter_hydration", L{"Starter hydration", "Anstellgut-Hydration"}, "%", 100, 50, 200),
			num("salt", L{"Salt", "Salz"}, "%", 2, 1, 3),
		},
		run: func(v calcVals) ([]calcOut, error) {
			f := v.n("flour")
			starter := f * v.n("levain") / 100
			sFlour := starter / (1 + v.n("starter_hydration")/100)
			sWater := starter - sFlour
			water := f*v.n("hydration")/100 - sWater
			if water < 0 {
				return nil, fmt.Errorf("that much starter is wetter than the target hydration; use less starter or a higher hydration")
			}
			return []calcOut{
				out("starter", "Starter", "Anstellgut", starter, "g"),
				out("add_flour", "Flour to add", "Mehl dazu", f-sFlour, "g"),
				out("add_water", "Water to add", "Wasser dazu", water, "g"),
				out("salt", "Salt", "Salz", f*v.n("salt")/100, "g"),
				out("total", "Dough in total", "Teig gesamt", f+f*v.n("hydration")/100+f*v.n("salt")/100, "g"),
			}, nil
		},
	},
	{
		ID: "vinaigrette", Title: L{"Vinaigrette", "Vinaigrette"},
		Desc: L{"Oil to acid by ratio (classic is 3:1), with mustard to hold it together.", "Öl zu Säure nach Verhältnis (klassisch 3:1), mit Senf als Emulgator."},
		Fields: []calcField{
			num("total_ml", L{"Dressing", "Dressing"}, "ml", 100, 20, 1000),
			num("ratio", L{"Oil parts per 1 part acid", "Teile Öl auf 1 Teil Säure"}, "", 3, 1, 6),
			pick("mustard", L{"Mustard", "Senf"}, "yes", op("yes", "yes", "ja"), op("no", "no", "nein")),
			num("salt_per_100", L{"Salt per 100 ml", "Salz pro 100 ml"}, "g", 1, 0, 3),
		},
		run: func(v calcVals) ([]calcOut, error) {
			t, r := v.n("total_ml"), v.n("ratio")
			o := []calcOut{
				out("oil", "Oil", "Öl", t*r/(r+1), "ml"),
				out("acid", "Vinegar or juice", "Essig oder Saft", t/(r+1), "ml"),
				out("salt", "Salt", "Salz", t/100*v.n("salt_per_100"), "g"),
			}
			if v.s("mustard") == "yes" {
				o = append(o, out("mustard", "Mustard", "Senf", t/100*5, "g"))
			}
			return o, nil
		},
	},
	{
		ID: "sauce_thicken", Title: L{"Thicken a sauce", "Sauce binden"},
		Desc: L{"Flour-and-butter roux or a cornstarch slurry for a given volume.", "Mehlschwitze oder Speisestärke für eine bestimmte Menge."},
		Fields: []calcField{
			num("volume_ml", L{"Liquid", "Flüssigkeit"}, "ml", 500, 100, 5000),
			pick("thickness", L{"Thickness", "Konsistenz"}, "medium", op("thin", "thin (pouring)", "dünn (gießbar)"), op("medium", "medium (coats a spoon)", "mittel (löffelfest)"), op("thick", "thick (béchamel)", "dick (Béchamel)")),
		},
		run: func(v calcVals) ([]calcOut, error) {
			// ponytail: g of flour per litre by thickness; cornstarch binds about twice as hard.
			perL := map[string]float64{"thin": 30, "medium": 50, "thick": 80}[v.s("thickness")]
			flour := perL * v.n("volume_ml") / 1000
			return []calcOut{
				out("roux_flour", "Roux: flour", "Schwitze: Mehl", flour, "g"),
				out("roux_fat", "Roux: butter", "Schwitze: Butter", flour, "g"),
				out("starch", "Or cornstarch", "Oder Speisestärke", flour/2, "g"),
				out("starch_water", "…stirred into cold water", "…in kaltem Wasser verrührt", flour/2*2, "ml"),
			}, nil
		},
	},
	{
		ID: "sauce_reduce", Title: L{"Reduce a sauce", "Sauce einkochen"},
		Desc: L{"How much to boil off, and how much saltier it gets.", "Wie viel einkochen und wie viel salziger es wird."},
		Fields: []calcField{
			num("volume_ml", L{"Start", "Start"}, "ml", 1000, 100, 10000),
			num("target_ml", L{"Target", "Ziel"}, "ml", 500, 50, 10000),
		},
		run: func(v calcVals) ([]calcOut, error) {
			from, to := v.n("volume_ml"), v.n("target_ml")
			if to >= from {
				return nil, fmt.Errorf("the target must be less than the start")
			}
			note := &L{"Salt after reducing, not before.", "Erst nach dem Einkochen salzen."}
			return []calcOut{
				out("boil_off", "Boil off", "Verdampfen", from-to, "ml"),
				out("left", "Left", "Übrig", to/from*100, "%"),
				{Key: "concentration", Label: L{"Salt gets", "Salz wird"}, Value: round(from/to, "x"), Unit: "×", Note: note},
			}, nil
		},
	},
	{
		ID: "salt", Title: L{"Salt", "Salz"},
		Desc: L{"Salt for a dry brine, wet brine, fermenting vegetables or cooking water. Not for nitrite curing.", "Salz für Trockenpökeln, Salzlake, Fermentieren oder Kochwasser. Nicht fürs Nitritpökeln."},
		Fields: []calcField{
			pick("mode", L{"For", "Für"}, "dry_brine",
				op("dry_brine", "dry-brining meat or fish", "Fleisch/Fisch trocken salzen"),
				op("wet_brine", "wet brine", "Salzlake"),
				op("ferment", "fermenting vegetables", "Gemüse fermentieren"),
				op("water", "pasta or vegetable water", "Nudel-/Gemüsewasser")),
			num("amount", L{"Weight (or water)", "Gewicht (bzw. Wasser)"}, "g", 1000, 10, 20000),
			num("percent", L{"Salt % (0 = usual for this)", "Salz % (0 = üblich dafür)"}, "%", 0, 0, 15),
		},
		run: func(v calcVals) ([]calcOut, error) {
			pct := v.n("percent")
			if pct == 0 {
				pct = map[string]float64{"dry_brine": 1.2, "wet_brine": 6, "ferment": 2, "water": 1}[v.s("mode")]
			}
			salt := v.n("amount") * pct / 100
			return []calcOut{
				out("percent", "Salt used", "Salzanteil", pct, "%"),
				out("salt", "Salt", "Salz", salt, "g"),
				out("sodium", "…of which sodium", "…davon Natrium", salt*393, "mg"),
			}, nil
		},
	},
}
