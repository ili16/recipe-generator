import { RecipeDocument } from '../types';

type Translate = (key: string, options?: Record<string, unknown>) => string;

// One-line, human summary of what changed between two consecutive snapshots. entries are
// newest-first (as returned by the history endpoint), so `prev` is the chronologically
// earlier one at index+1. `t` is passed in rather than read off the i18n singleton: this is
// a pure function, and its caller already has one.
export function summarizeVersionChange(curr: RecipeDocument, prev: RecipeDocument | undefined, t: Translate): string {
  if (!prev) return t('history.initialVersion');
  const changes: string[] = [];
  if (curr.title !== prev.title) changes.push(t('history.renamedTo', { title: curr.title }));
  if (curr.servings !== prev.servings) changes.push(t('history.servings', { from: prev.servings ?? '—', to: curr.servings ?? '—' }));
  if (curr.total_minutes !== prev.total_minutes) changes.push(t('history.total', { from: prev.total_minutes ?? '—', to: curr.total_minutes ?? '—' }));
  if (curr.prep_minutes !== prev.prep_minutes) changes.push(t('history.prep', { from: prev.prep_minutes ?? '—', to: curr.prep_minutes ?? '—' }));
  if (curr.cook_minutes !== prev.cook_minutes) changes.push(t('history.cook', { from: prev.cook_minutes ?? '—', to: curr.cook_minutes ?? '—' }));
  if (curr.difficulty !== prev.difficulty) changes.push(t('history.difficultyChanged'));
  if ((curr.summary ?? '') !== (prev.summary ?? '')) changes.push(t('history.summaryUpdated'));

  const prevIngredients = new Set(prev.ingredients.map(i => i.item.trim().toLowerCase()));
  const currIngredients = new Set(curr.ingredients.map(i => i.item.trim().toLowerCase()));
  const added = [...currIngredients].filter(i => !prevIngredients.has(i)).length;
  const removed = [...prevIngredients].filter(i => !currIngredients.has(i)).length;
  if (added) changes.push(t('history.ingredientsAdded', { count: added }));
  if (removed) changes.push(t('history.ingredientsRemoved', { count: removed }));

  if (curr.steps.length !== prev.steps.length) {
    const diff = curr.steps.length - prev.steps.length;
    changes.push(t(diff > 0 ? 'history.stepsAdded' : 'history.stepsRemoved', { count: Math.abs(diff) }));
  } else if (curr.steps.some((s, i) => s.step_text !== prev.steps[i]?.step_text)) {
    changes.push(t('history.stepsUpdated'));
  }

  return changes.length ? changes.join(' · ') : t('history.minorEdit');
}
