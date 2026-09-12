package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
)

// newTestSession builds a session with one draft parked in it, so tools taking a
// draft_ref have something to resolve without an LLM call.
func newTestSession(userID int, draft model.RecipeDocument) *chatSession {
	return &chatSession{
		id:        "test-conversation",
		userID:    userID,
		drafts:    map[string]model.RecipeDocument{"draft_1": draft},
		nextDraft: 1,
	}
}

var testDraft = model.RecipeDocument{
	Title:       "Test Pasta",
	Language:    "en",
	Ingredients: []model.RecipeIngredient{{Item: "Pasta"}},
	Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Boil it"}},
}

// TestToolCatalogIsWellFormed checks every entry in the catalog is actually callable and
// actually declarable: a runner, a description, and a JSON-schema object the API will
// accept. A tool that only half-exists fails the turn at runtime, not at review.
func TestToolCatalogIsWellFormed(t *testing.T) {
	if len(chatTools) == 0 {
		t.Fatal("catalog is empty")
	}
	for name, tool := range chatTools {
		if tool.Run == nil {
			t.Errorf("%s: no Run", name)
		}
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s: no description", name)
		}
		if tool.Params["type"] != "object" {
			t.Errorf("%s: params must be a JSON-schema object, got %v", name, tool.Params["type"])
		}
		props, ok := tool.Params["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s: params has no properties map", name)
		}
		required, ok := tool.Params["required"].([]string)
		if !ok {
			t.Fatalf("%s: params has no required list", name)
		}
		for _, r := range required {
			if _, ok := props[r]; !ok {
				t.Errorf("%s: required arg %q is not declared in properties", name, r)
			}
		}
	}

	defs := toolDefinitions()
	if len(defs) != len(chatTools) {
		t.Errorf("toolDefinitions() rendered %d of %d tools", len(defs), len(chatTools))
	}
	for _, d := range defs {
		if _, ok := chatTools[d.Function.Name]; !ok {
			t.Errorf("toolDefinitions() declared unknown tool %q", d.Function.Name)
		}
	}
}

// TestToolsRefuseAnonymousCallers drives every tool in the catalog as an anonymous
// caller. Every tool that touches durable per-user state must refuse before it runs —
// there is no user to scope it to, so running it at all would be the bug.
func TestToolsRefuseAnonymousCallers(t *testing.T) {
	session := newTestSession(0, testDraft)
	for name, tool := range chatTools {
		if !tool.NeedsAuth {
			continue
		}
		t.Run(name, func(t *testing.T) {
			// Deliberately valid-looking args: the refusal must come from the missing
			// identity, not from argument parsing.
			_, _, err := dispatch(context.Background(), session, name, `{"id":1,"date":"2026-09-14","starts_on":"2026-09-14","draft_ref":"draft_1","patch":{"skill_level":"beginner"},"target":"draft_1","instruction":"x"}`)
			if !errors.Is(err, errSignInRequired) {
				t.Errorf("expected sign-in refusal, got %v", err)
			}
		})
	}
}

func TestDispatchUnknownToolIsAnError(t *testing.T) {
	if _, _, err := dispatch(context.Background(), newTestSession(1, testDraft), "delete_everything", `{}`); err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
}

// TestToolsOwnershipComesFromTheSession is the rule the whole catalog rests on: a tool
// arg naming another user's recipe id must resolve to not-found, never to that recipe
// (specs/AGENT_CHAT.md §2.2). Runs against a real DB — skipped unless DB_URL is set.
func TestToolsOwnershipComesFromTheSession(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	storage.InitDBPool()
	ctx := context.Background()

	var ownerID, otherID int
	if err := storage.Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-tools-owner') RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, ownerID) })
	if err := storage.Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-tools-other') RETURNING id`).Scan(&otherID); err != nil {
		t.Fatalf("seed other: %v", err)
	}
	t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherID) })

	secret := testDraft
	secret.Title = "Owner's Secret Curry"
	recipe, err := storage.AddRecipeStructured(ctx, ownerID, secret, "# Owner's Secret Curry", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("seed recipe: %v", err)
	}
	t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })
	id := strconv.Itoa(recipe.ID)

	intruder := newTestSession(otherID, testDraft)

	t.Run("get_recipe", func(t *testing.T) {
		result, _, err := dispatch(ctx, intruder, "get_recipe", `{"id":`+id+`}`)
		if err == nil {
			t.Fatalf("expected not-found, got result %v", result)
		}
		if strings.Contains(err.Error(), "Curry") {
			t.Errorf("error leaked the other user's recipe: %v", err)
		}
	})

	t.Run("transform_recipe", func(t *testing.T) {
		// resolveRecipe must fail before any LLM call happens.
		if _, err := intruder.resolveRecipe(ctx, id); err == nil {
			t.Fatal("resolved another user's recipe")
		}
	})

	t.Run("set_plan_day", func(t *testing.T) {
		if _, _, err := dispatch(ctx, intruder, "set_plan_day", `{"date":"2026-09-14","recipe_id":`+id+`}`); err == nil {
			t.Fatal("planned another user's recipe")
		}
	})

	t.Run("apply_week_plan", func(t *testing.T) {
		if _, _, err := dispatch(ctx, intruder, "apply_week_plan", `{"days":[{"date":"2026-09-14","recipe_id":`+id+`}],"confirm":true}`); err == nil {
			t.Fatal("planned another user's recipe")
		}
	})

	// The whole point of 4.3: applying is atomic, idempotent, and asks before it
	// replaces a day the user already filled.
	t.Run("apply is atomic, idempotent and confirms overwrites", func(t *testing.T) {
		owner := newTestSession(ownerID, testDraft)
		t.Cleanup(func() {
			storage.Pool.Exec(ctx, `DELETE FROM meal_plans WHERE user_id = $1`, ownerID)
			storage.Pool.Exec(ctx, `DELETE FROM recipes WHERE user_id = $1 AND id <> $2`, ownerID, recipe.ID)
		})

		// A day whose recipe is fine plus one naming a recipe the owner does not have:
		// the good day must not be written.
		_, _, err := dispatch(ctx, owner, "apply_week_plan",
			`{"days":[{"date":"2026-09-14","recipe_id":`+id+`},{"date":"2026-09-15","recipe_id":999999999}],"confirm":true}`)
		if err == nil {
			t.Fatal("expected the bad day to fail the apply")
		}
		items, err := storage.GetMealPlanRange(ctx, ownerID, date(t, "2026-09-14"), date(t, "2026-09-15"))
		if err != nil {
			t.Fatalf("read plan: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("a failed apply wrote %d item(s): %+v", len(items), items)
		}

		// Two days, one of them a draft that has to be saved first.
		args := `{"days":[{"date":"2026-09-14","recipe_id":` + id + `},{"date":"2026-09-15","draft_ref":"draft_1"}],"confirm":true}`
		if _, _, err := dispatch(ctx, owner, "apply_week_plan", args); err != nil {
			t.Fatalf("apply: %v", err)
		}
		// Applying the same plan again must neither duplicate the draft's recipe nor
		// need confirming — nothing actually changes.
		result, _, err := dispatch(ctx, owner, "apply_week_plan", strings.Replace(args, `,"confirm":true`, "", 1))
		if err != nil {
			t.Fatalf("re-apply: %v", err)
		}
		if applied, _ := result.(map[string]any)["applied"].(bool); !applied {
			t.Errorf("re-applying an identical plan asked for confirmation: %v", result)
		}
		var drafts int
		if err := storage.Pool.QueryRow(ctx,
			`SELECT count(*) FROM recipes WHERE user_id = $1 AND title = $2`, ownerID, testDraft.Title,
		).Scan(&drafts); err != nil {
			t.Fatalf("count drafts: %v", err)
		}
		if drafts != 1 {
			t.Errorf("re-applying saved the draft %d times", drafts)
		}

		// Changing a filled day is reported, not silently done.
		result, _, err = dispatch(ctx, owner, "apply_week_plan", `{"days":[{"date":"2026-09-15","recipe_id":`+id+`}]}`)
		if err != nil {
			t.Fatalf("overwrite check: %v", err)
		}
		raw, _ := json.Marshal(result)
		if !strings.Contains(string(raw), "confirm_required") || strings.Contains(string(raw), `"applied":true`) {
			t.Errorf("overwriting a planned day was not confirmed first: %s", raw)
		}
	})

	t.Run("search_my_recipes", func(t *testing.T) {
		result, _, err := dispatch(ctx, intruder, "search_my_recipes", `{"query":"Curry"}`)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "Curry") {
			t.Errorf("search returned another user's recipe: %s", raw)
		}
	})

	// The owner, by contrast, gets their own recipe back.
	t.Run("owner still sees it", func(t *testing.T) {
		result, art, err := dispatch(ctx, newTestSession(ownerID, testDraft), "get_recipe", `{"id":`+id+`}`)
		if err != nil {
			t.Fatalf("owner could not read their own recipe: %v", err)
		}
		if art == nil || art.Kind != "recipe" {
			t.Errorf("expected a recipe artifact, got %+v", art)
		}
		raw, _ := json.Marshal(result)
		if !strings.Contains(string(raw), "Curry") {
			t.Errorf("owner's recipe missing from result: %s", raw)
		}
	})
}

// TestToolsRejectBadArguments covers the other half of dispatch: model-generated
// arguments are untrusted text, so a malformed or empty one must come back as a tool
// error the model can recover from, never a panic or a partial write.
func TestToolsRejectBadArguments(t *testing.T) {
	session := newTestSession(1, testDraft)
	cases := []struct{ tool, args string }{
		{"generate_recipe", `{}`},                                      // neither description nor url
		{"generate_recipe", `not json`},                                //
		{"fetch_url", `{}`},                                            // no url
		{"answer_cooking_question", `{"question":"  "}`},               // blank question
		{"transform_recipe", `{"target":"draft_9","instruction":"x"}`}, // no such draft
	}
	for _, c := range cases {
		t.Run(c.tool+" "+c.args, func(t *testing.T) {
			if _, _, err := dispatch(context.Background(), session, c.tool, c.args); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		t.Fatalf("bad date %q: %v", s, err)
	}
	return d
}

// TestResolvePlanDays covers the argument half of apply_week_plan, which is untrusted
// model output: a day list that would write the wrong thing must be refused, not applied.
func TestResolvePlanDays(t *testing.T) {
	session := newTestSession(1, testDraft)
	for _, c := range []struct{ name, days string }{
		{"empty", `[]`},
		{"no recipe", `[{"date":"2026-09-14"}]`},
		{"bad date", `[{"date":"next tuesday","recipe_id":1}]`},
		{"same day twice", `[{"date":"2026-09-14","recipe_id":1},{"date":"2026-09-14","recipe_id":2}]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var days []planDayArg
			if err := json.Unmarshal([]byte(c.days), &days); err != nil {
				t.Fatalf("bad fixture: %v", err)
			}
			if _, err := session.resolvePlanDays(context.Background(), days); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestSessionDrafts(t *testing.T) {
	session := &chatSession{drafts: map[string]model.RecipeDocument{}}
	result, art, err := session.addDraft(testDraft, &model.GenerationOrigin{SourceType: "url", SourceURL: "https://example.com/x"})
	if err != nil {
		t.Fatalf("addDraft: %v", err)
	}
	summary := result.(map[string]any)
	if summary["draft_ref"] != "draft_1" || summary["title"] != "Test Pasta" {
		t.Errorf("unexpected draft summary: %v", summary)
	}
	// The summary handed to the model is a summary — the full document goes to the
	// client as an artifact instead of costing tokens on every later iteration.
	if _, leaked := summary["ingredients"].([]model.RecipeIngredient); leaked {
		t.Error("draft summary carries the full ingredient list")
	}
	if art == nil || art.Kind != "recipe" {
		t.Fatalf("expected a recipe artifact, got %+v", art)
	}
	// Provenance is parked with the draft, since saveDraft is the only thing that can
	// write it and by then the URL is long gone (BACKLOG.md 3.7).
	if got := session.draftInfo["draft_1"].origin; got == nil || got.SourceURL != "https://example.com/x" {
		t.Errorf("draft lost its origin: %+v", got)
	}
	if _, err := session.resolveRecipe(context.Background(), "draft_1"); err != nil {
		t.Errorf("resolveRecipe(draft_1): %v", err)
	}
	if _, err := session.resolveRecipe(context.Background(), "draft_2"); err == nil {
		t.Error("resolved a draft that was never created")
	}

	// plan_week parks one draft per variant day so "apply it" has something
	// apply_week_plan can name; two variants must not collide on one ref.
	first, second := session.parkDraft(testDraft, 0, nil), session.parkDraft(testDraft, 0, nil)
	if first == second {
		t.Fatalf("parkDraft reused ref %q", first)
	}
	for _, ref := range []string{first, second} {
		if _, err := session.resolveRecipe(context.Background(), ref); err != nil {
			t.Errorf("resolveRecipe(%s): %v", ref, err)
		}
	}
}

func TestMergePreferences(t *testing.T) {
	skill := "beginner"
	current := model.UserPreferences{
		SkillLevel:         &skill,
		DietaryPrefs:       []string{"vegetarian"},
		MealPlanBatchDays:  1,
		WeekStartDay:       "monday",
		MealPlanNoCookDays: []string{"wednesday"},
	}
	merged, err := mergePreferences(current, map[string]any{
		"dietary_prefs": []any{"vegan"},
		"not_a_field":   "ignored",
	})
	if err != nil {
		t.Fatalf("mergePreferences: %v", err)
	}
	if len(merged.DietaryPrefs) != 1 || merged.DietaryPrefs[0] != "vegan" {
		t.Errorf("patch did not replace dietary_prefs: %v", merged.DietaryPrefs)
	}
	// Untouched fields survive: a patch is not a replacement.
	if merged.SkillLevel == nil || *merged.SkillLevel != "beginner" || merged.WeekStartDay != "monday" ||
		len(merged.MealPlanNoCookDays) != 1 || merged.MealPlanBatchDays != 1 {
		t.Errorf("patch clobbered untouched fields: %+v", merged)
	}
}
