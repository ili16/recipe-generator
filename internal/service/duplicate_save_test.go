package service

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
)

// TestSaveRecipeDuplicatePrompt is BACKLOG.md 8.3's acceptance, end to end through the
// tool the model actually calls: saving a draft that came from a URL already in the
// library reports the match instead of saving; the second call saves anyway; and taking
// the variant option links the new recipe to the one it duplicated.
// Runs against a real DB — skipped unless DB_URL is set.
func TestSaveRecipeDuplicatePrompt(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	storage.InitDBPool()
	ctx := context.Background()

	var userID int
	if err := storage.Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-dup-save') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	const url = "https://example.com/ragu"
	origin := &model.GenerationOrigin{SourceType: "url", SourceURL: url}

	first := testDraft
	first.Title = "Slow Ragu"
	saved, err := storage.AddRecipeStructured(ctx, userID, first, "# Slow Ragu", origin, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("seed recipe: %v", err)
	}

	// A second draft off the same URL, deliberately retitled so only the URL matches.
	second := testDraft
	second.Title = "Ragu, Again"
	newSession := func() *chatSession {
		s := newTestSession(userID, second)
		s.draftInfo = map[string]draftInfo{"draft_1": {origin: origin}}
		return s
	}

	call := func(t *testing.T, s *chatSession, args string) map[string]any {
		t.Helper()
		res, _, err := dispatch(ctx, s, "save_recipe", args)
		if err != nil {
			t.Fatalf("save_recipe(%s): %v", args, err)
		}
		m, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("save_recipe returned %T, want a map", res)
		}
		return m
	}

	t.Run("prompts once with the match", func(t *testing.T) {
		s := newSession()
		got := call(t, s, `{"draft_ref":"draft_1"}`)
		dup, ok := got["duplicate_of"].(*storage.DuplicateMatch)
		if !ok {
			t.Fatalf("no duplicate_of in %v", got)
		}
		if dup.RecipeID != saved.ID || dup.MatchedOn != "url" {
			t.Errorf("matched %+v, want recipe %d on url", dup, saved.ID)
		}
		if got["saved"] != false {
			t.Error("reported a save; nothing should have been written")
		}
		// The report must be JSON — it goes back to the model as a tool result.
		if _, err := json.Marshal(got); err != nil {
			t.Fatalf("tool result is not JSON: %v", err)
		}
	})

	t.Run("declining still saves", func(t *testing.T) {
		s := newSession()
		got := call(t, s, `{"draft_ref":"draft_1","confirm":true}`)
		id, _ := got["recipe_id"].(int)
		if id == 0 || id == saved.ID {
			t.Fatalf("expected a new recipe, got %v", got)
		}
		t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, id) })

		var variantOf *int
		if err := storage.Pool.QueryRow(ctx, `SELECT variant_of_recipe_id FROM recipes WHERE id = $1`, id).Scan(&variantOf); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if variantOf != nil {
			t.Errorf("saved as a variant of %d; the user declined that", *variantOf)
		}
	})

	t.Run("variant links to the original", func(t *testing.T) {
		s := newSession()
		got := call(t, s, `{"draft_ref":"draft_1","variant_of":`+strconv.Itoa(saved.ID)+`}`)
		id, _ := got["recipe_id"].(int)
		if id == 0 {
			t.Fatalf("expected a saved recipe, got %v", got)
		}
		t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, id) })

		var variantOf *int
		if err := storage.Pool.QueryRow(ctx, `SELECT variant_of_recipe_id FROM recipes WHERE id = $1`, id).Scan(&variantOf); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if variantOf == nil || *variantOf != saved.ID {
			t.Errorf("variant_of_recipe_id = %v, want %d", variantOf, saved.ID)
		}
	})

	t.Run("another user's recipe cannot be named as the parent", func(t *testing.T) {
		s := newSession()
		if _, _, err := dispatch(ctx, s, "save_recipe", `{"draft_ref":"draft_1","variant_of":`+strconv.Itoa(saved.ID+100000)+`}`); err == nil {
			t.Error("expected not-found for an id that isn't the caller's")
		}
	})

	// The seeded recipe is deleted last: the sub-tests read it.
	t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, saved.ID) })
}
