import type { Recipe } from '../../types';

/**
 * The three cheap library filters from BACKLOG 6.5 — all of them read fields the list
 * payload already carries, so none of them costs a request.
 */
export type QuickFilter = 'favourite' | 'fast' | 'batch' | 'pantry';

// i18n keys, not words — resolved with `t()` where the chips are rendered (see src/i18n).
export const QUICK_FILTER_LABEL_KEYS: Record<QuickFilter, string> = {
  favourite: 'recipes.quickFilter.favourite',
  fast: 'recipes.quickFilter.fast',
  batch: 'recipes.quickFilter.batch',
  pantry: 'recipes.quickFilter.pantry',
};

// A recipe with no time or servings recorded is not "fast" and not "batch" — unknown is
// excluded rather than guessed, so a filter never promises something it can't back.
/** Share of a recipe's ingredients the pantry holds; 0 when it lists none (BACKLOG 16.7). */
export const pantryRatio = (r: Recipe): number =>
  (r.pantry_total ?? 0) > 0 ? (r.pantry_have ?? 0) / r.pantry_total! : 0;

const MATCHES: Record<QuickFilter, (r: Recipe) => boolean> = {
  favourite: r => r.my_vote === 1,
  fast: r => (r.total_minutes ?? 0) > 0 && r.total_minutes! < 25,
  batch: r => (r.servings ?? 0) >= 4,
  pantry: r => pantryRatio(r) >= 0.8,
};

/** Every selected filter must match — they narrow, like the tag filters above them. */
export const matchesQuickFilters = (recipe: Recipe, active: Set<QuickFilter>): boolean =>
  Array.from(active).every(f => MATCHES[f](recipe));
