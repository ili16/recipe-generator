/**
 * Self-check for the thread grouping. The two things that can be wrong are the day
 * boundary (a thread from 23:00 last night must not be "Today" at 08:00) and the
 * accent folding in search.
 *
 *   npx tsc src/utils/threadGroups.check.ts --outDir /tmp/tg --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/tg/threadGroups.check.js
 */
import { strict as assert } from 'assert';
import { groupThreads, matchesQuery } from './threadGroups';

const t = (updated_at: string, title = '', preview = 'p') => ({ title, preview, updated_at });
const labels = { today: 'Today', yesterday: 'Yesterday', locale: 'en-GB' };

export function check(): void {
  // Local 2026-09-18 08:00. The 23:00 thread is last night, not this morning.
  const now = new Date(2026, 8, 18, 8, 0);
  const groups = groupThreads(
    [
      t(new Date(2026, 8, 18, 7, 30).toISOString(), 'a'),
      t(new Date(2026, 8, 18, 0, 5).toISOString(), 'b'),
      t(new Date(2026, 8, 17, 23, 0).toISOString(), 'c'),
      t(new Date(2026, 8, 12, 9, 0).toISOString(), 'd'),
      t(new Date(2025, 8, 12, 9, 0).toISOString(), 'e'),
    ],
    { now, ...labels },
  );
  assert.deepEqual(groups.map((g) => g.label), ['Today', 'Yesterday', '12 Sept', '12 Sept 2025']);
  // Same-day threads collapse into one group rather than one group each.
  assert.deepEqual(groups[0].threads.map((x) => x.title), ['a', 'b']);
  assert.equal(groups[3].threads.length, 1);
  assert.deepEqual(groupThreads([], { now, ...labels }), []);

  // Search: empty matches everything, accents and case are ignored, preview counts.
  assert.equal(matchesQuery(t('', 'Grünkohl'), '  '), true);
  assert.equal(matchesQuery(t('', 'Grünkohl'), 'grunkohl'), true);
  assert.equal(matchesQuery(t('', '', 'pasta bake'), 'BAKE'), true);
  assert.equal(matchesQuery(t('', 'Grünkohl', 'x'), 'soup'), false);

  console.log('threadGroups.check: all assertions passed');
}

check();
