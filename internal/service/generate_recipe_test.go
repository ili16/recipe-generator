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
