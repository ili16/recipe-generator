package llm

import "testing"

// TestPromptVersionChangesWithContent guards the one invariant that matters for
// recipes.ai_prompt_version: the hash must be deterministic and must change if any of
// the prompts it covers changes. We can't mutate the embedded files at test time, so
// this checks the hash function's actual behavior directly instead.
func TestPromptVersionChangesWithContent(t *testing.T) {
	h := func(a, b, c, d string) string { return promptHash(a, b, c, d) }

	base := h("structured", "note", "refine", "variant")
	again := h("structured", "note", "refine", "variant")
	if base != again {
		t.Fatalf("hash not deterministic: %q vs %q", base, again)
	}

	changed := h("structured v2", "note", "refine", "variant")
	if changed == base {
		t.Fatal("changing one input did not change the hash")
	}
}

func TestPromptsNonEmpty(t *testing.T) {
	prompts := map[string]string{
		"StructuredRecipePrompt":     StructuredRecipePrompt,
		"NoCarryOverTimeNote":        NoCarryOverTimeNote,
		"RefineJudgmentAddendum":     RefineJudgmentAddendum,
		"VariantAddendum":            VariantAddendum,
		"MealPlanSuggestBase":        MealPlanSuggestBase,
		"MealPlanLowVarietyAddendum": MealPlanLowVarietyAddendum,
	}
	for name, content := range prompts {
		if content == "" {
			t.Errorf("%s: embedded prompt is empty — go:embed path likely wrong", name)
		}
	}
}
