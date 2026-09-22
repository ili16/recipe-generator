/**
 * Self-check for `offlineRecipes.ts` — the cooking-mode offline cache (BACKLOG 12.4).
 *
 * The two things worth a check are the eviction (a cache that grows forever fills a
 * phone) and `isOffline` (get it wrong and a server outage tells the cook to check
 * their wifi, or a dead wifi tells them nothing at all).
 *
 * No test runner in this repo (see `AGENT.md`), so this is a plain assert script:
 *
 *   npx tsc src/utils/offlineRecipes.check.ts --outDir /tmp/or --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/or/offlineRecipes.check.js
 *
 * Nothing imports it, so it never reaches a bundle; `npx tsc --noEmit` typechecks it.
 */
import { strict as assert } from 'assert';
import { mergeCookCache, isOffline } from './offlineRecipes';
import { Recipe } from '../types';

const r = (id: number, name = `r${id}`) => ({ id, recipename: name } as Recipe);

export function check(): void {
  // Fresh first, and it replaces the stale copy of itself rather than sitting beside it.
  const merged = mergeCookCache([r(1, 'old'), r(2)], [r(1, 'new')]);
  assert.deepEqual(merged.map(x => x.id), [1, 2]);
  assert.equal(merged[0].recipename, 'new', 'the copy that just came off the server wins');

  // Order within each half is preserved: most recently opened stays nearest the front.
  assert.deepEqual(mergeCookCache([r(3), r(4)], [r(1), r(2)]).map(x => x.id), [1, 2, 3, 4]);

  // The cap evicts the oldest, which is the tail of `cached`.
  assert.deepEqual(mergeCookCache([r(2), r(3)], [r(1)], 2).map(x => x.id), [1, 2]);
  assert.equal(mergeCookCache([], [], 2).length, 0);

  // A repeated id inside one list is still one entry.
  assert.deepEqual(mergeCookCache([], [r(1), r(1)]).map(x => x.id), [1]);

  // No status means the request never reached the server.
  assert.equal(isOffline({ status: undefined }), true);
  assert.equal(isOffline({}), false, 'a plain error is not evidence of no network');
  assert.equal(isOffline({ status: 500 }), false, 'a broken server is not a dead wifi');
  assert.equal(isOffline({ status: 404 }), false);
  assert.equal(isOffline(null), false);
  assert.equal(isOffline(undefined), false);

  console.log('offlineRecipes.check: ok');
}

if (require.main === module) check();
