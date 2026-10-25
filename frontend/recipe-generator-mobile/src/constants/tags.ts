// Fixed recipe tagging taxonomy, mirroring the backend's internal/service/tags.go and
// db/migrations/00006_recipe_tags.sql. See recipe-generator/specs/RECIPE_TAGS.md.

export type TagGroup =
  | 'meal_type'
  | 'dietary'
  | 'time_difficulty'
  | 'cooking_method'
  | 'primary_ingredient'
  | 'cuisine_vibe';

export interface TagDef {
  slug: string;
  group: TagGroup;
}

// The slugs are the backend contract (internal/service/tags.go) and never change; the words
// shown for them live in `src/i18n` under `tags.slug.<slug>` and `tags.group.<group>`.
export const tagLabelKey = (slug: string) => `tags.slug.${slug}`;
export const tagGroupLabelKey = (group: TagGroup) => `tags.group.${group}`;

export const ALL_TAGS: TagDef[] = [
  // Order matters: mealTypeSlug picks the first of these a recipe carries, so the tags
  // saying *when* it is eaten come before the ones saying what it is.
  { slug: 'breakfast-brunch', group: 'meal_type' },
  { slug: 'lunch', group: 'meal_type' },
  { slug: 'dinner', group: 'meal_type' },
  { slug: 'snacks', group: 'meal_type' },
  { slug: 'appetizers-starters', group: 'meal_type' },
  { slug: 'main-courses', group: 'meal_type' },
  { slug: 'side-dishes', group: 'meal_type' },
  { slug: 'desserts', group: 'meal_type' },
  { slug: 'beverages', group: 'meal_type' },
  { slug: 'sauces-dressings', group: 'meal_type' },

  { slug: 'vegan', group: 'dietary' },
  { slug: 'vegetarian', group: 'dietary' },
  { slug: 'gluten-free', group: 'dietary' },
  { slug: 'dairy-free', group: 'dietary' },
  { slug: 'nut-free', group: 'dietary' },
  { slug: 'keto', group: 'dietary' },
  { slug: 'low-carb', group: 'dietary' },

  { slug: 'under-20-minutes', group: 'time_difficulty' },
  { slug: '30-minute-meals', group: 'time_difficulty' },
  { slug: 'easy-prep', group: 'time_difficulty' },
  { slug: 'beginner-friendly', group: 'time_difficulty' },
  { slug: 'meal-prep', group: 'time_difficulty' },

  { slug: 'one-pot-one-pan', group: 'cooking_method' },
  { slug: 'no-cook', group: 'cooking_method' },
  { slug: 'baking', group: 'cooking_method' },
  { slug: 'air-fryer', group: 'cooking_method' },
  { slug: 'slow-cooker', group: 'cooking_method' },
  { slug: 'instant-pot', group: 'cooking_method' },
  { slug: 'grilling', group: 'cooking_method' },

  { slug: 'chicken-poultry', group: 'primary_ingredient' },
  { slug: 'beef-red-meat', group: 'primary_ingredient' },
  { slug: 'pork', group: 'primary_ingredient' },
  { slug: 'seafood', group: 'primary_ingredient' },
  { slug: 'pasta-grains', group: 'primary_ingredient' },
  { slug: 'vegetables', group: 'primary_ingredient' },

  { slug: 'comfort-food', group: 'cuisine_vibe' },
  { slug: 'healthy', group: 'cuisine_vibe' },
  { slug: 'budget-friendly', group: 'cuisine_vibe' },
  { slug: 'kid-friendly', group: 'cuisine_vibe' },
  { slug: 'spicy', group: 'cuisine_vibe' },
];

export const TAGS_BY_GROUP: Record<TagGroup, TagDef[]> = ALL_TAGS.reduce(
  (acc, tag) => {
    acc[tag.group].push(tag);
    return acc;
  },
  {
    meal_type: [],
    dietary: [],
    time_difficulty: [],
    cooking_method: [],
    primary_ingredient: [],
    cuisine_vibe: [],
  } as Record<TagGroup, TagDef[]>
);

// Which meal-type tags make a recipe a likely candidate for a given slot. Used to *rank*
// the picker, never to filter it — people eat pizza for breakfast (BACKLOG 9.9).
export const SLOT_TAGS: Record<string, string[]> = {
  breakfast: ['breakfast-brunch', 'beverages'],
  lunch: ['lunch', 'main-courses', 'side-dishes', 'appetizers-starters'],
  dinner: ['dinner', 'main-courses', 'side-dishes', 'sauces-dressings'],
  snack: ['snacks', 'desserts', 'appetizers-starters', 'beverages'],
};

// The one tag the library card leads with: which meal this is. Returns the first meal-type
// tag in taxonomy order, so a recipe tagged both `dinner` and `main-courses` reads "Dinner".
// null for a recipe carrying no meal-type tag at all — render nothing, not a placeholder.
// The caller turns the slug into words with `t(tagLabelKey(slug))`.
export function mealTypeSlug(tags?: string[] | null): string | null {
  if (!tags?.length) return null;
  return TAGS_BY_GROUP.meal_type.find((t) => tags.includes(t.slug))?.slug ?? null;
}
