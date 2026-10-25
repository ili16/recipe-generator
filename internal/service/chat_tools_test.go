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
			_, _, err := dispatch(context.Background(), session, name, `{"id":1,"date":"`+day1+`","starts_on":"`+day1+`","draft_ref":"draft_1","patch":{"skill_level":"beginner"},"target":"draft_1","instruction":"x"}`)
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
		if _, _, err := dispatch(ctx, intruder, "set_plan_day", `{"date":"`+day1+`","recipe_id":`+id+`}`); err == nil {
			t.Fatal("planned another user's recipe")
		}
	})

	t.Run("apply_week_plan", func(t *testing.T) {
		if _, _, err := dispatch(ctx, intruder, "apply_week_plan", `{"days":[{"date":"`+day1+`","recipe_id":`+id+`}],"confirm":true}`); err == nil {
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
			`{"days":[{"date":"`+day1+`","recipe_id":`+id+`},{"date":"`+day2+`","recipe_id":999999999}],"confirm":true}`)
		if err == nil {
			t.Fatal("expected the bad day to fail the apply")
		}
		items, err := storage.GetMealPlanRange(ctx, ownerID, date(t, day1), date(t, day2))
		if err != nil {
			t.Fatalf("read plan: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("a failed apply wrote %d item(s): %+v", len(items), items)
		}

		// Two days, one of them a draft that has to be saved first.
		args := `{"days":[{"date":"` + day1 + `","recipe_id":` + id + `},{"date":"` + day2 + `","draft_ref":"draft_1"}],"confirm":true}`
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
		result, _, err = dispatch(ctx, owner, "apply_week_plan", `{"days":[{"date":"`+day2+`","recipe_id":`+id+`}]}`)
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

// day1 and day2 are tomorrow and the day after, not fixed dates. The plan tools refuse
// to write to a day in the past, so a hardcoded date is a test that passes until that
// date goes by — this suite broke exactly that way.
var (
	day1 = time.Now().AddDate(0, 0, 1).Format(time.DateOnly)
	day2 = time.Now().AddDate(0, 0, 2).Format(time.DateOnly)
)

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
		{"same day and slot twice", `[{"date":"2026-09-14","recipe_id":1,"meal_slot":"lunch"},{"date":"2026-09-14","recipe_id":2,"meal_slot":"lunch"}]`},
		{"bad slot", `[{"date":"2026-09-14","recipe_id":1,"meal_slot":"brunch"}]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			var days []planDayArg
			if err := json.Unmarshal([]byte(c.days), &days); err != nil {
				t.Fatalf("bad fixture: %v", err)
			}
			if _, _, err := session.resolvePlanDays(context.Background(), days); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestSessionDrafts(t *testing.T) {
	session := &chatSession{drafts: map[string]model.RecipeDocument{}}
	result, art, err := session.addDraft(testDraft, 0, &model.GenerationOrigin{SourceType: "url", SourceURL: "https://example.com/x"}, "")
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
	// A fresh draft arrives with its card; display_recipe renders the same one.
	if art == nil || art.Kind != "recipe" {
		t.Fatalf("addDraft emitted no recipe card: %+v", art)
	}
	art = displayArtifact(t, session, "draft_1")
	// Provenance is parked with the draft, since saveDraft is the only thing that can
	// write it and by then the URL is long gone (BACKLOG.md 3.7).
	if got := session.draftInfo["draft_1"].origin; got == nil || got.SourceURL != "https://example.com/x" {
		t.Errorf("draft lost its origin: %+v", got)
	}
	if _, err := session.resolveRecipe(context.Background(), "draft_1"); err != nil {
		t.Errorf("resolveRecipe(draft_1): %v", err)
	}
	// A fresh generation replaces nothing, so it must carry no derived_from — the client
	// keys its "what changed" block on that field's presence (BACKLOG.md 10.3), and an
	// empty one would make every new recipe look like an edit of something.
	if _, ok := art.Data.(map[string]any)["derived_from"]; ok {
		t.Error("a fresh draft must not claim to be derived from anything")
	}
	if _, err := session.resolveRecipe(context.Background(), "draft_2"); err == nil {
		t.Error("resolved a draft that was never created")
	}

	// A transform does: it names the card it changed, which is how the client finds the
	// before-document it already holds.
	if _, _, err := session.addDraft(testDraft, 0, nil, "draft_1"); err != nil {
		t.Fatalf("addDraft(derived): %v", err)
	}
	derived := displayArtifact(t, session, "draft_2")
	if got := derived.Data.(map[string]any)["derived_from"]; got != "draft_1" {
		t.Errorf("derived_from = %v, want draft_1", got)
	}

	// translate_recipe parks its result as a variant of the saved recipe it read, so
	// saving files a German sibling instead of replacing the English original, and the
	// duplicate check (which skips variants) does not stop it on the shared title.
	_, _, err = session.addDraft(testDraft, 7, nil, "recipe:7")
	if err != nil {
		t.Fatalf("addDraft(variant): %v", err)
	}
	if got := session.draftInfo["draft_3"].variantOf; got != 7 {
		t.Errorf("variantOf = %d, want 7", got)
	}

	// Which parent that is comes straight off the target ref: a draft has none.
	for ref, want := range map[string]int{"recipe:7": 7, "7": 7, " recipe:7 ": 7, "draft_1": 0, "": 0, "recipe:-1": 0} {
		if got := savedRecipeID(ref); got != want {
			t.Errorf("savedRecipeID(%q) = %d, want %d", ref, got, want)
		}
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

// TestApplyNotedPreference is BACKLOG 11.1's list logic: the write is one value, it is
// case-insensitive both ways, and it never claims a change it did not make.
func TestApplyNotedPreference(t *testing.T) {
	current := model.UserPreferences{
		DislikedIngredients: []string{"cilantro"},
		DietaryPrefs:        []string{"vegetarian"},
	}

	added, list, changed, err := applyNotedPreference(current, "disliked_ingredients", " Mushrooms ", false)
	if err != nil || !changed || len(list) != 2 || list[1] != "Mushrooms" {
		t.Fatalf("add: %v changed=%v list=%v", err, changed, list)
	}
	if len(added.DietaryPrefs) != 1 {
		t.Errorf("add clobbered the other list: %+v", added)
	}

	if _, _, changed, err := applyNotedPreference(added, "disliked_ingredients", "MUSHROOMS", false); err != nil || changed {
		t.Errorf("re-adding the same value should be a no-op: changed=%v err=%v", changed, err)
	}

	removed, list, changed, err := applyNotedPreference(added, "disliked_ingredients", "mushrooms", true)
	if err != nil || !changed || len(list) != 1 || list[0] != "cilantro" {
		t.Fatalf("remove: %v changed=%v list=%v", err, changed, list)
	}
	if _, _, changed, _ := applyNotedPreference(removed, "disliked_ingredients", "mushrooms", true); changed {
		t.Errorf("removing what is not there should be a no-op")
	}

	if _, _, _, err := applyNotedPreference(current, "household_size", "3", false); err == nil {
		t.Errorf("a field that is not a list should be refused")
	}
	if _, _, _, err := applyNotedPreference(current, "disliked_ingredients", "  ", false); err == nil {
		t.Errorf("an empty value should be refused")
	}
}

// TestResolvePlanDaysSlots is BACKLOG 6.10's acceptance: one call can fill two slots of
// the same day, and a day that names no slot still goes to dinner.
func TestResolvePlanDaysSlots(t *testing.T) {
	session := newTestSession(1, testDraft)
	tomorrow := today().AddDate(0, 0, 1).Format(time.DateOnly)

	days, _, err := session.resolvePlanDays(context.Background(), []planDayArg{
		{Date: tomorrow, RecipeID: 1, MealSlot: "lunch"},
		{Date: tomorrow, RecipeID: 2},
	})
	if err != nil {
		t.Fatalf("lunch plus dinner on one day: %v", err)
	}
	if len(days) != 2 || days[0].MealSlot != "lunch" || days[1].MealSlot != storage.DefaultMealSlot {
		t.Errorf("expected a lunch and a defaulted dinner, got %+v", days)
	}
}

// TestResolvePlanDaysSkipsPast pins the bug report: replanning the current week must not
// rewrite the days already cooked, and a list of nothing but past days is an error.
func TestResolvePlanDaysSkipsPast(t *testing.T) {
	session := newTestSession(1, testDraft)
	yesterday := today().AddDate(0, 0, -1).Format(time.DateOnly)
	tomorrow := today().AddDate(0, 0, 1).Format(time.DateOnly)

	days, past, err := session.resolvePlanDays(context.Background(),
		[]planDayArg{{Date: yesterday, RecipeID: 1}, {Date: tomorrow, RecipeID: 2}})
	if err != nil {
		t.Fatalf("mixed range: %v", err)
	}
	if len(days) != 1 || days[0].RecipeID != 2 {
		t.Errorf("expected only the future day, got %+v", days)
	}
	if len(past) != 1 || past[0] != yesterday {
		t.Errorf("expected %s reported as past, got %v", yesterday, past)
	}

	if _, _, err := session.resolvePlanDays(context.Background(),
		[]planDayArg{{Date: yesterday, RecipeID: 1}}); err == nil {
		t.Error("a past-only day list should be refused")
	}
}

// displayArtifact runs display_recipe for one ref and returns the card it emitted.
func displayArtifact(t *testing.T, session *chatSession, ref string) *artifact {
	t.Helper()
	_, art, err := toolDisplayRecipe(context.Background(), session, json.RawMessage(`{"target":"`+ref+`"}`))
	if err != nil {
		t.Fatalf("display_recipe(%s): %v", ref, err)
	}
	if art == nil || art.Kind != "recipe" {
		t.Fatalf("display_recipe(%s) emitted no recipe card: %+v", ref, art)
	}
	return art
}

// A recipe the model never displays must stay off the screen — and one it asks for that
// does not exist is an error it can report, not a blank card.
func TestDisplayRecipeRejectsUnknownRef(t *testing.T) {
	session := &chatSession{drafts: map[string]model.RecipeDocument{}}
	if _, art, err := toolDisplayRecipe(context.Background(), session, json.RawMessage(`{"target":"draft_9"}`)); err == nil || art != nil {
		t.Errorf("displayed a draft that does not exist: art=%+v err=%v", art, err)
	}
}

// TestLastUserText pins what generate_recipe's from_message hands the recipe model: the
// latest user message as typed, minus the attachment markers HandleChat appends.
func TestLastUserText(t *testing.T) {
	pasted := "Baguette\nZutaten\n    150 g lauwarmes Wasser\n    Eine Prise Instant-Hefe"
	msgs := []model.ConversationMessage{
		{Role: "user", Content: "an older message"},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: pasted + "\n\n[attached link] https://example.com\n\n[attached photo] image_ref=image1"},
		{Role: "tool", Content: "{}"},
	}
	if got := lastUserText(msgs); got != pasted {
		t.Fatalf("lastUserText = %q, want %q", got, pasted)
	}
	if got := lastUserText(nil); got != "" {
		t.Fatalf("lastUserText(nil) = %q, want empty", got)
	}
}
