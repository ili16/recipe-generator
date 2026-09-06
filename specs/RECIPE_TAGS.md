# Recipe Tags

Replaces the single free-text `recipes.category` column (`main|starter|bread|dessert|other`)
with a fixed, multi-group tagging taxonomy. A recipe can carry any number of tags across any
number of groups (e.g. `main-courses` + `vegan` + `30-minute-meals` + `air-fryer`).

## Taxonomy

Fixed vocabulary, ~38 tags across 6 groups. This list is the single source of truth — mirrored
as a Go constant (`internal/service` schema/validation) and a TS constant
(`src/constants/tags.ts`, frontend filter UI). It is not user-editable and has no admin
endpoint; adding a tag means updating both constants plus a migration seed row.

| Group (`tag_group`) | Label | Slug |
|---|---|---|
| meal_type | Breakfast & Brunch | `breakfast-brunch` |
| meal_type | Appetizers & Starters | `appetizers-starters` |
| meal_type | Main Courses | `main-courses` |
| meal_type | Side Dishes | `side-dishes` |
| meal_type | Desserts | `desserts` |
| meal_type | Snacks | `snacks` |
| meal_type | Beverages | `beverages` |
| meal_type | Sauces & Dressings | `sauces-dressings` |
| dietary | Vegan | `vegan` |
| dietary | Vegetarian | `vegetarian` |
| dietary | Gluten-Free | `gluten-free` |
| dietary | Dairy-Free | `dairy-free` |
| dietary | Nut-Free | `nut-free` |
| dietary | Keto | `keto` |
| dietary | Low-Carb | `low-carb` |
| time_difficulty | Under 20 Minutes | `under-20-minutes` |
| time_difficulty | 30-Minute Meals | `30-minute-meals` |
| time_difficulty | Easy Prep | `easy-prep` |
| time_difficulty | Beginner Friendly | `beginner-friendly` |
| time_difficulty | Meal Prep | `meal-prep` |
| cooking_method | One-Pot / One-Pan | `one-pot-one-pan` |
| cooking_method | No-Cook | `no-cook` |
| cooking_method | Baking | `baking` |
| cooking_method | Air Fryer | `air-fryer` |
| cooking_method | Slow Cooker | `slow-cooker` |
| cooking_method | Instant Pot | `instant-pot` |
| cooking_method | Grilling | `grilling` |
| primary_ingredient | Chicken & Poultry | `chicken-poultry` |
| primary_ingredient | Beef & Red Meat | `beef-red-meat` |
| primary_ingredient | Pork | `pork` |
| primary_ingredient | Seafood | `seafood` |
| primary_ingredient | Pasta & Grains | `pasta-grains` |
| primary_ingredient | Vegetables | `vegetables` |
| cuisine_vibe | Comfort Food | `comfort-food` |
| cuisine_vibe | Healthy | `healthy` |
| cuisine_vibe | Budget-Friendly | `budget-friendly` |
| cuisine_vibe | Kid-Friendly | `kid-friendly` |
| cuisine_vibe | Spicy | `spicy` |

## Schema

```sql
CREATE TABLE tags (
    id SERIAL PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    tag_group TEXT NOT NULL CHECK (tag_group IN (
        'meal_type','dietary','time_difficulty','cooking_method','primary_ingredient','cuisine_vibe'
    )),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE recipe_tags (
    recipe_id INT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    tag_id    INT NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (recipe_id, tag_id)
);
CREATE INDEX idx_recipe_tags_tag ON recipe_tags (tag_id);
```

Follows the existing `techniques`/`recipe_techniques` reference-vocabulary + join-table
pattern (`db/migrations/00004_reference_and_planning.sql`), minus the `confidence` column —
no per-tag confidence scoring is needed for a closed vocabulary picked directly by the
extraction LLM.

`recipes.category` is dropped in the same migration (`db/migrations/00006_recipe_tags.sql`),
after backfilling `recipe_tags` from the old value via a best-effort mapping:

| old `category` | new tag |
|---|---|
| `main` | `main-courses` |
| `starter` | `appetizers-starters` |
| `dessert` | `desserts` |
| `bread` | `side-dishes` (no exact equivalent) |
| `other` | *(no tag — untagged)* |

This is a single-deploy, pre-release breaking change (per `REFACTOR_PLAN.md` scope
guardrails) — no dual-read/dual-write compatibility period.

## API Contract Change

Every place `category: string` appeared, it becomes `tags: string[]` (a list of slugs):

- `RecipeDocument.category` → `RecipeDocument.tags` (structured generation output,
  `POST /generate`, `POST /refine-recipe`)
- `Recipe.category` → `Recipe.tags` (API response envelope, `GET /get-recipes`,
  `POST /add-recipe`, `PATCH /update-recipe`)
- `POST /add-recipe` request: `category` → `tags`
- `PATCH /update-recipe` request: `category` → `tags` — an empty/omitted `tags` means
  "leave existing tags unchanged" (same convention the old `category` field used via
  `COALESCE(NULLIF($3,''), category)`); a non-empty list replaces the recipe's tags
  entirely.

## LLM Tagging

- **Structured generation** (`recipeJSONSchema`, used by `/generate` and `/refine-recipe`):
  the `tags` field is a JSON-schema array of the 38 slugs above (still a single
  extraction call, no separate tagging pass). The system prompt instructs the model to
  apply every group that's genuinely applicable (course, dietary, time/difficulty, method,
  primary ingredient, vibe) rather than picking just one value, since a recipe can and
  usually should carry several tags across groups.
- **Legacy plain-markdown save path** (`classifyTags`, used only when a client posts
  `POST /add-recipe` without `structured`): a single cheap-model call asking for a
  comma-separated list of matching slugs from the fixed vocabulary, parsed by checking
  which known slugs appear in the response text. Defaults to no tags on any LLM error.
