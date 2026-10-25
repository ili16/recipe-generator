/**
 * Self-check for the library's quick filters. No test runner in this repo (see `AGENT.md`):
 *
 *   npx tsc src/screens/recipes/quickFilters.check.ts --outDir /tmp/qf --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/qf/screens/recipes/quickFilters.check.js
 *
 * Nothing imports it, so it never reaches a bundle; `npx tsc --noEmit` still typechecks it.
 */
import { strict as assert } from 'assert';
import type { Recipe } from '../../types';
import { matchesQuickFilters, QuickFilter } from './quickFilters';

const r = (over: Partial<Recipe>): Recipe => ({ id: 1, recipename: 'x', recipe: '', ...over });
const m = (recipe: Recipe, ...f: QuickFilter[]) => matchesQuickFilters(recipe, new Set(f));

export function check(): void {
  // No filters selected: everything passes.
  assert.equal(m(r({})), true);

  assert.equal(m(r({ my_vote: 1 }), 'favourite'), true);
  assert.equal(m(r({ my_vote: -1 }), 'favourite'), false);
  assert.equal(m(r({}), 'favourite'), false);

  assert.equal(m(r({ total_minutes: 20 }), 'fast'), true);
  assert.equal(m(r({ total_minutes: 25 }), 'fast'), false);
  // Unknown time is excluded, not guessed — and a 0 is unknown, not instant.
  assert.equal(m(r({}), 'fast'), false);
  assert.equal(m(r({ total_minutes: 0 }), 'fast'), false);

  assert.equal(m(r({ servings: 4 }), 'batch'), true);
  assert.equal(m(r({ servings: 2 }), 'batch'), false);
  assert.equal(m(r({}), 'batch'), false);

  // 80% is inclusive; no ingredients or an empty pantry never qualifies.
  assert.equal(m(r({ pantry_have: 4, pantry_total: 5 }), 'pantry'), true);
  assert.equal(m(r({ pantry_have: 3, pantry_total: 5 }), 'pantry'), false);
  assert.equal(m(r({ pantry_have: 0, pantry_total: 0 }), 'pantry'), false);

  // Filters narrow: both must hold.
  assert.equal(m(r({ my_vote: 1, total_minutes: 15 }), 'favourite', 'fast'), true);
  assert.equal(m(r({ my_vote: 1, total_minutes: 90 }), 'favourite', 'fast'), false);

  console.log('quickFilters: all checks pass');
}

check();
