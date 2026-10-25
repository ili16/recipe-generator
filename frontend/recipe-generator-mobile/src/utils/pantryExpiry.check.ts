/**
 * Self-check for the pantry's expiry maths (BACKLOG 7.4) — the "use first" flag and the
 * badge both hang off `daysUntil`, and an off-by-one there silently flags the wrong food.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/utils/pantryExpiry.check.ts --outDir /tmp/pe --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/pe/pantryExpiry.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` still typechecks it.
 */
import { strict as assert } from 'assert';
import { daysUntil, expiryLabel, stockCheckDue, USE_FIRST_DAYS, STOCK_CHECK_DAYS } from './pantryExpiry';

const iso = (d: Date): string =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;

const shift = (from: Date, days: number): Date => {
  const d = new Date(from);
  d.setDate(d.getDate() + days);
  return d;
};

export function check(): void {
  // Late in the evening is the case a naive now-to-midnight difference gets wrong.
  for (const hour of [0, 8, 23]) {
    const now = new Date(2026, 8, 12, hour, 30);
    assert.equal(daysUntil(iso(now), now), 0, `today at ${hour}:30`);
    assert.equal(daysUntil(iso(shift(now, 1)), now), 1, `tomorrow at ${hour}:30`);
    assert.equal(daysUntil(iso(shift(now, -1)), now), -1, `yesterday at ${hour}:30`);
    assert.equal(daysUntil(iso(shift(now, USE_FIRST_DAYS)), now), USE_FIRST_DAYS, `edge at ${hour}:30`);
    assert.equal(daysUntil(iso(shift(now, USE_FIRST_DAYS + 1)), now), USE_FIRST_DAYS + 1, `past edge at ${hour}:30`);
  }

  // Month and year boundaries, and a DST changeover — the rounding is what survives the
  // 23- and 25-hour days a plain division would leave at 0.96 and 1.04.
  const eve = new Date(2026, 11, 31, 18, 0);
  assert.equal(daysUntil('2027-01-01', eve), 1, 'across new year');
  const dst = new Date(2026, 2, 28, 12, 0); // EU clocks go forward on the 29th
  assert.equal(daysUntil('2026-03-30', dst), 2, 'across a DST change');

  // The words come from the catalogs; what this pins is which key each bucket picks.
  const key = (k: string) => k;
  assert.equal(expiryLabel(-2, key), 'pantry.expired');
  assert.equal(expiryLabel(0, key), 'pantry.today');
  assert.equal(expiryLabel(1, key), 'pantry.tomorrow');
  assert.equal(expiryLabel(4, key), 'pantry.inDays');

  // The stock check dates a timestamp, not a calendar day: 27 days is still trusted.
  const now = new Date(2026, 8, 20, 12, 0);
  const daysAgo = (n: number) => new Date(now.getTime() - n * 86400000).toISOString();
  assert.equal(stockCheckDue(daysAgo(27), now), false, 'inside the window');
  assert.equal(stockCheckDue(daysAgo(STOCK_CHECK_DAYS), now), true, 'at the window');
  assert.equal(stockCheckDue(daysAgo(60), now), true, 'well past it');
  assert.equal(stockCheckDue(undefined, now), false, 'no timestamp, no nag');
  assert.equal(stockCheckDue('not a date', now), false, 'unparseable, no nag');

  console.log('pantryExpiry.check: all assertions passed');
}

check();
