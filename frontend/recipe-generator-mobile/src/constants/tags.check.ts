/**
 * Self-check for `mealTypeSlug` — the library card's lead badge. It resolves a slug list
 * in taxonomy order, so a reordering of ALL_TAGS silently changes which meal a recipe
 * claims to be; that is what this pins.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/constants/tags.check.ts --outDir /tmp/tg --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/tg/tags.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` typechecks it.
 */
import { strict as assert } from 'assert';
import { mealTypeSlug } from './tags';

export function check(): void {
  // Picks the meal-type tag out of a mixed list, whatever order the recipe carries them in.
  assert.equal(mealTypeSlug(['vegan', 'one-pot-one-pan', 'dinner', 'spicy']), 'dinner');
  assert.equal(mealTypeSlug(['lunch']), 'lunch');

  // Taxonomy order decides, not the recipe's: a dish tagged both when-eaten and what-it-is
  // reads as the meal, and breakfast wins over a later slot.
  assert.equal(mealTypeSlug(['main-courses', 'dinner']), 'dinner');
  assert.equal(mealTypeSlug(['dinner', 'breakfast-brunch']), 'breakfast-brunch');

  // Recipes saved before migration 00031 have no slot tag — they fall back to what they have.
  assert.equal(mealTypeSlug(['main-courses', 'vegan']), 'main-courses');

  // Nothing to say: render no badge rather than an empty one.
  assert.equal(mealTypeSlug(['nut-free', 'easy-prep']), null);
  assert.equal(mealTypeSlug([]), null);
  assert.equal(mealTypeSlug(undefined), null);

  console.log('tags.check: all assertions passed');
}

check();
