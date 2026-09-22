package llm

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

// Prompts live as files under prompts/, not string literals, so they can be reviewed,
// diffed, and hashed (see PromptVersion) independent of the Go code that composes them.
// Each composition below preserves the prefix-caching shape the live calls depend on: a
// static block first, any variable suffix appended after it, never interleaved (see
// specs/RECIPE_QUALITY_IMPLEMENTATION.md §3).

//go:embed prompts/structured_recipe.txt
var StructuredRecipePrompt string

// NoCarryOverTimeNote is appended (not prepended) wherever a previous document is
// already in context — refine and variant generation — so the model recomputes timing
// from the new document's own steps instead of keeping the prior document's numbers.
//
//go:embed prompts/no_carry_over_time_note.txt
var NoCarryOverTimeNote string

//go:embed prompts/refine_judgment_addendum.txt
var RefineJudgmentAddendum string

//go:embed prompts/variant_addendum.txt
var VariantAddendum string

// CookFlowAddendum turns the structured-recipe prompt into a steps-only rewrite: group
// the steps into mise en place / cook / wait, merge the ones too thin to stand on a
// screen, and mark what runs at the same time. Appended after StructuredRecipePrompt
// (never interleaved) because it leans on that file's granularity/phase/parallelism rules
// rather than restating them.
//
//go:embed prompts/cook_flow.txt
var CookFlowAddendum string

//go:embed prompts/meal_plan_suggest_base.txt
var MealPlanSuggestBase string

//go:embed prompts/meal_plan_low_variety_addendum.txt
var MealPlanLowVarietyAddendum string

// ChatAgentSystemPrompt governs the agent loop behind POST /api/v1/chat. It is the
// whole static prefix of every turn — thread history and tool results are appended
// after it, never interleaved — so the cacheable prefix stays byte-identical.
//
//go:embed prompts/chat_agent_system.txt
var ChatAgentSystemPrompt string

// PantryScanPrompt reads a photo of a shelf or a shop receipt into pantry lines
// (BACKLOG.md 17.5). Not part of PromptVersion: it shapes a pantry row, never a saved
// RecipeDocument.
//
//go:embed prompts/pantry_scan.txt
var PantryScanPrompt string

// VideoFramesPrompt reads the text burned into a cooking reel's frames (BACKLOG.md
// 11.10) — the half the soundtrack leaves out, because the voiceover says "a good glug"
// while the screen says "2 tbsp". Not part of PromptVersion: it produces the raw text
// that feeds a generation, never a RecipeDocument itself.
//
//go:embed prompts/video_frames.txt
var VideoFramesPrompt string

// ThreadSummaryPrompt compresses the dropped span of an over-long thread into one
// system message (BACKLOG.md 2.6). Cheap tier — it summarises text that is already in
// hand, not a recipe.
//
//go:embed prompts/thread_summary.txt
var ThreadSummaryPrompt string

// ThreadTitlePrompt names a thread from its first user message so it can be found again
// in the conversation list (BACKLOG.md 10.4). Cheap tier, one call per thread, ever.
//
//go:embed prompts/thread_title.txt
var ThreadTitlePrompt string

// promptHash is a short deterministic fingerprint of the given prompt contents,
// concatenated in order.
func promptHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// PromptVersion is a content hash of every prompt file that shapes a saved
// RecipeDocument — fresh generation, refine, and variant — computed once from the
// embedded files. Replaces the old hand-bumped promptVersion = "v4" constant (which
// covered only 2 of them): this one changes when, and only when, one of these files
// changes, with no commit-time discipline required.
var PromptVersion = promptHash(StructuredRecipePrompt, NoCarryOverTimeNote, RefineJudgmentAddendum, VariantAddendum, CookFlowAddendum)
