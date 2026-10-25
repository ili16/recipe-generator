import { RecipeDocument } from '../types';
import { fmtIngredient } from './recipeIngredient';

/**
 * What changed between two versions of the same recipe (BACKLOG.md 10.3). "Make it vegan"
 * returns a whole new document, and without this the card just re-renders and the user has
 * to spot the difference themselves.
 *
 * Client-side on purpose: this needs no model output, no prompt and no schema — the server
 * only says *which* earlier card this one came from (`derived_from`), and the client
 * already holds that card.
 *
 * Ingredients are keyed on the normalised item name, which is what makes "250 g flour →
 * 200 g flour" a change rather than a removal plus an addition. Steps are compared by
 * position: a transform rewrites steps in place far more often than it reorders them, and
 * a real sequence alignment for the rare reorder is not worth the code.
 */
export interface RecipeDiff {
  ingredients: {
    added: string[];
    removed: string[];
    changed: Array<{ from: string; to: string }>;
  };
  steps: {
    added: number;
    removed: number;
    /** 1-based positions whose text was rewritten. */
    changed: number[];
  };
  /** Header values that moved. `from`/`to` are already display strings. */
  fields: Array<{ key: 'title' | 'servings' | 'total_minutes'; from: string; to: string }>;
}

const key = (item: string) => item.trim().toLowerCase();

const show = (v: number | null | undefined) => (v == null ? '—' : String(v));

export function diffRecipes(prev: RecipeDocument, next: RecipeDocument): RecipeDiff {
  const before = new Map(prev.ingredients.map((i) => [key(i.item), i]));
  const after = new Map(next.ingredients.map((i) => [key(i.item), i]));

  const added: string[] = [];
  const changed: Array<{ from: string; to: string }> = [];
  for (const [k, ing] of after) {
    const was = before.get(k);
    if (!was) {
      added.push(fmtIngredient(ing));
      continue;
    }
    const from = fmtIngredient(was);
    const to = fmtIngredient(ing);
    if (from !== to) changed.push({ from, to });
  }
  const removed = [...before].filter(([k]) => !after.has(k)).map(([, ing]) => fmtIngredient(ing));

  const overlap = Math.min(prev.steps.length, next.steps.length);
  const stepsChanged: number[] = [];
  for (let i = 0; i < overlap; i++) {
    if (prev.steps[i].step_text.trim() !== next.steps[i].step_text.trim()) stepsChanged.push(i + 1);
  }

  const fields: RecipeDiff['fields'] = [];
  if (prev.title !== next.title) fields.push({ key: 'title', from: prev.title, to: next.title });
  if (prev.servings !== next.servings) {
    fields.push({ key: 'servings', from: show(prev.servings), to: show(next.servings) });
  }
  if (prev.total_minutes !== next.total_minutes) {
    fields.push({ key: 'total_minutes', from: show(prev.total_minutes), to: show(next.total_minutes) });
  }

  return {
    ingredients: { added, removed, changed },
    steps: {
      added: Math.max(0, next.steps.length - prev.steps.length),
      removed: Math.max(0, prev.steps.length - next.steps.length),
      changed: stepsChanged,
    },
    fields,
  };
}

/** Nothing moved — the card renders no diff at all rather than an empty "Changes" block. */
export const isEmptyDiff = (d: RecipeDiff): boolean =>
  d.ingredients.added.length === 0 &&
  d.ingredients.removed.length === 0 &&
  d.ingredients.changed.length === 0 &&
  d.steps.added === 0 &&
  d.steps.removed === 0 &&
  d.steps.changed.length === 0 &&
  d.fields.length === 0;

/** How many individual lines the diff holds — the count on the collapsed summary row. */
export const diffSize = (d: RecipeDiff): number =>
  d.ingredients.added.length +
  d.ingredients.removed.length +
  d.ingredients.changed.length +
  d.steps.changed.length +
  (d.steps.added > 0 ? 1 : 0) +
  (d.steps.removed > 0 ? 1 : 0) +
  d.fields.length;
