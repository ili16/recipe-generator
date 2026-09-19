package service

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

func TestResolveLanguage(t *testing.T) {
	cases := []struct {
		name, prompt, preferred, want string
	}{
		{"short foreign dish name defers to browser locale", "sauce bearnaise", "en", "en"},
		{"long clear-language sentence overrides browser locale", "ein rezept für sauce bearnaise", "en", "de"},
		{"empty prompt falls back to browser locale", "", "en", "en"},
		{"no browser locale, ambiguous prompt falls back to legacy self-detect", "sauce bearnaise", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveLanguage(c.prompt, c.preferred); got != c.want {
				t.Errorf("resolveLanguage(%q, %q) = %q, want %q", c.prompt, c.preferred, got, c.want)
			}
		})
	}
}

// TestRecipeSchemaCoversStructFields guards against recipeJSONSchema() — an untyped,
// hand-built map[string]any — drifting out of sync with the model structs it mirrors.
// Adding a field to model.RecipeDocument/RecipeIngredient/RecipeStep without a matching
// schema entry fails this test instead of only failing at the live OpenAI call (strict
// mode requires every "properties" entry also listed in "required").
func TestRecipeSchemaCoversStructFields(t *testing.T) {
	schema := recipeJSONSchema()
	assertSchemaMatchesStruct(t, "recipeJSONSchema", schema, reflect.TypeOf(model.RecipeDocument{}))

	props := schema["properties"].(map[string]any)
	ingredientItems := props["ingredients"].(map[string]any)["items"].(map[string]any)
	assertSchemaMatchesStruct(t, "recipeJSONSchema.ingredients.items", ingredientItems, reflect.TypeOf(model.RecipeIngredient{}))

	stepItems := props["steps"].(map[string]any)["items"].(map[string]any)
	assertSchemaMatchesStruct(t, "recipeJSONSchema.steps.items", stepItems, reflect.TypeOf(model.RecipeStep{}))
}

func assertSchemaMatchesStruct(t *testing.T, name string, schema map[string]any, typ reflect.Type) {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: missing properties", name)
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("%s: missing required", name)
	}
	requiredSet := make(map[string]bool, len(required))
	for _, r := range required {
		requiredSet[r] = true
	}

	wantFields := structJSONFields(typ)
	for _, f := range wantFields {
		if _, ok := props[f]; !ok {
			t.Errorf("%s: model.%s field %q has no schema property — schema drifted from the struct", name, typ.Name(), f)
		}
		if !requiredSet[f] {
			t.Errorf("%s: %q missing from required (strict mode needs every property listed)", name, f)
		}
	}
	if len(props) != len(wantFields) {
		t.Errorf("%s: schema has %d properties, model.%s has %d fields — drifted", name, len(props), typ.Name(), len(wantFields))
	}
}

func structJSONFields(typ reflect.Type) []string {
	fields := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fields = append(fields, name)
	}
	return fields
}

// TestSameIngredients guards the one thing a cook-flow review must never do. Rewriting
// how a dish is cooked is the whole point of the pass; quietly rewriting what goes into
// it would be a far worse bug than a badly planned flow, so ReviewCookFlow rejects a
// result whose ingredients moved. Compared on what a shopper would notice — item,
// quantity, unit, optional — and deliberately not on section/category, which a
// regeneration may legitimately fill in where the original left them null.
func TestSameIngredients(t *testing.T) {
	num := func(f float64) *float64 { return &f }
	str := func(s string) *string { return &s }
	base := []model.RecipeIngredient{
		{Item: "Pasta", Quantity: num(500), Unit: str("g")},
		{Item: "Olive oil", Quantity: num(30), Unit: str("ml"), Optional: true},
	}
	// Same list with the nullable annotations filled in — still the same shopping.
	annotated := []model.RecipeIngredient{
		{Item: "pasta ", Quantity: num(500), Unit: str("g"), Section: str("Dry"), Category: str("pantry")},
		{Item: "Olive oil", Quantity: num(30), Unit: str("ml"), Optional: true, Category: str("pantry")},
	}

	cases := []struct {
		name string
		got  []model.RecipeIngredient
		want bool
	}{
		{"identical", base, true},
		{"case and whitespace only", annotated, true},
		{"quantity changed", []model.RecipeIngredient{
			{Item: "Pasta", Quantity: num(400), Unit: str("g")}, base[1],
		}, false},
		{"unit changed", []model.RecipeIngredient{
			{Item: "Pasta", Quantity: num(500), Unit: str("kg")}, base[1],
		}, false},
		{"quantity dropped to null", []model.RecipeIngredient{
			{Item: "Pasta", Unit: str("g")}, base[1],
		}, false},
		{"item renamed", []model.RecipeIngredient{
			{Item: "Spaghetti", Quantity: num(500), Unit: str("g")}, base[1],
		}, false},
		{"optional flipped", []model.RecipeIngredient{
			base[0], {Item: "Olive oil", Quantity: num(30), Unit: str("ml")},
		}, false},
		{"one dropped", base[:1], false},
		{"one added", append(append([]model.RecipeIngredient{}, base...),
			model.RecipeIngredient{Item: "Garlic", Quantity: num(10), Unit: str("g")}), false},
		{"reordered", []model.RecipeIngredient{base[1], base[0]}, false},
	}
	for _, tc := range cases {
		if got := sameIngredients(base, tc.got); got != tc.want {
			t.Errorf("%s: sameIngredients = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// BACKLOG 14.3: the words RenderMarkdown adds are display text and follow the recipe's
// language. An unknown language falls back to English rather than dropping the label.
func TestRenderMarkdownLabelsFollowLanguage(t *testing.T) {
	i, s, c := 30, 4, 10
	doc := model.RecipeDocument{
		Title: "Test", Language: "de", Servings: &s, TotalMinutes: &i, ProteinG: &c,
		Ingredients: []model.RecipeIngredient{{Item: "Salz", Optional: true}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Kochen"}},
	}
	de := RenderMarkdown(doc)
	for _, want := range []string{"## Zutaten", "## Zubereitung", "Portionen: 4", "Gesamt: 30 Min.", "Eiweiß", "Pro Portion", "(optional)"} {
		if !strings.Contains(de, want) {
			t.Errorf("german render missing %q:\n%s", want, de)
		}
	}
	doc.Language = "xx"
	en := RenderMarkdown(doc)
	for _, want := range []string{"## Ingredients", "Servings: 4", "Total: 30 min", "protein"} {
		if !strings.Contains(en, want) {
			t.Errorf("fallback render missing %q:\n%s", want, en)
		}
	}
}
