/**
 * Self-check for the budget renderer. The only thing here that can be wrong is the month
 * rollover — a cap that "resets on 1 December" in December is a sentence that tells the
 * user to wait a year.
 *
 *   npx tsc src/utils/budget.check.ts --outDir /tmp/bud --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/bud/budget.check.js
 */
import { strict as assert } from 'assert';
import { resetsOn } from './budget';

export function check(): void {
  assert.equal(resetsOn('2026-09-01T00:00:00Z', 'en-US'), 'October 1');
  // December rolls into the next year, not into a 13th month.
  assert.equal(resetsOn('2026-12-01T00:00:00Z', 'en-US'), 'January 1');
  assert.equal(resetsOn('2026-09-01T00:00:00Z', 'de-DE'), '1. Oktober');
  console.log('budget.check: all assertions passed');
}

check();
