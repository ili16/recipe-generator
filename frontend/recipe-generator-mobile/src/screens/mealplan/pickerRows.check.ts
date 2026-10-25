/**
 * Self-check for `pickerRows` — the ordering that decides whether "Add a meal → Breakfast"
 * shows you breakfasts or shows you drinks and dinners (BACKLOG 9.9).
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/screens/mealplan/pickerRows.check.ts --outDir /tmp/pr --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/pr/screens/mealplan/pickerRows.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` still typechecks it.
 */
import { strict as assert } from 'assert';
import type { Recipe } from '../../types';
import { pickerRows, type PickerRow } from './pickerRows';

const r = (id: number, recipename: string, tags: string[]): Recipe =>
  ({ id, recipename, recipe: '', tags } as Recipe);

const names = (rows: PickerRow[]): string[] =>
  rows.map(row => row.kind === 'heading' ? `— ${row.labelKey}` : row.recipe.recipename);

export function check(): void {
  const pancakes = r(1, 'Pancakes', ['breakfast-brunch']);
  const stew = r(2, 'Beef stew', ['main-courses', 'beef-red-meat']);
  const untagged = r(3, 'Mystery jar', []);

  // The acceptance case: breakfast leads, dinner falls under the divider.
  assert.deepEqual(
    names(pickerRows([stew, pancakes], 'breakfast', '')),
    ['Pancakes', '— plan.otherRecipes', 'Beef stew'],
  );

  // Same library, other slot — the order flips rather than anything disappearing.
  assert.deepEqual(
    names(pickerRows([pancakes, stew], 'dinner', '')),
    ['Beef stew', '— plan.otherRecipes', 'Pancakes'],
  );

  // Ranked, never filtered: every recipe survives, tagged or not.
  assert.equal(pickerRows([stew, pancakes, untagged], 'snack', '').filter(x => x.kind === 'recipe').length, 3);

  // A divider needs both sides. All-likely and all-unlikely lists get none.
  assert.deepEqual(names(pickerRows([pancakes], 'breakfast', '')), ['Pancakes']);
  assert.deepEqual(names(pickerRows([stew, untagged], 'breakfast', '')), ['Beef stew', 'Mystery jar']);

  // No slot (Swap from a day view that has none) is save order, untouched.
  assert.deepEqual(names(pickerRows([stew, pancakes], undefined, '')), ['Beef stew', 'Pancakes']);

  // Search filters on the name, case-insensitively, and still ranks what is left.
  assert.deepEqual(names(pickerRows([stew, pancakes], 'breakfast', 'pan')), ['Pancakes']);
  assert.deepEqual(names(pickerRows([stew, pancakes], 'breakfast', '  BEEF ')), ['Beef stew']);
  assert.deepEqual(pickerRows([stew, pancakes], 'breakfast', 'zzz'), []);

  // structured.tags is the fallback when the list payload carries no top-level tags.
  const structuredOnly = { id: 4, recipename: 'Omelette', recipe: '', structured: { tags: ['breakfast-brunch'] } } as Recipe;
  assert.deepEqual(
    names(pickerRows([stew, structuredOnly], 'breakfast', '')),
    ['Omelette', '— plan.otherRecipes', 'Beef stew'],
  );

  console.log('pickerRows.check: 9 checks passed');
}

check();
