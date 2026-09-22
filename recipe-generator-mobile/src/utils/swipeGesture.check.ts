/**
 * Self-check for `swipeGesture.ts` — the decisions behind every swipe in the app.
 *
 * The one that matters is `shouldClaim`: get it wrong and the pantry list stops scrolling,
 * which is a far worse bug than a swipe that does not fire.
 *
 * No test runner in this repo (see `AGENT.md`), so this is a plain assert script:
 *
 *   npx tsc src/utils/swipeGesture.check.ts --outDir /tmp/sw --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/sw/swipeGesture.check.js
 *
 * Nothing imports it, so it never reaches a bundle; `npx tsc --noEmit` typechecks it.
 */
import { strict as assert } from 'assert';
import { shouldClaim, swipeEnd } from './swipeGesture';

export function check(): void {
  // A scroll is a scroll. This is the whole reason the function exists.
  assert.equal(shouldClaim('x', 6, 40), false, 'a vertical drag must never be claimed by a row');
  assert.equal(shouldClaim('x', 40, 6), true, 'a horizontal drag is ours');
  assert.equal(shouldClaim('y', 6, 40), true, 'the sheet takes the vertical one');
  assert.equal(shouldClaim('y', 40, 6), false);

  // A diagonal belongs to nobody in particular — and so to neither of us.
  assert.equal(shouldClaim('x', 30, 30), false, 'a 45° drag is ambiguous, leave it');
  assert.equal(shouldClaim('y', 30, 30), false);

  // Under the slop nothing is a swipe, however lopsided.
  assert.equal(shouldClaim('x', 9, 0), false, 'a twitch is not a swipe');
  assert.equal(shouldClaim('x', 11, 0), true);

  // Release: far enough, or fast enough and a third of the way.
  assert.equal(swipeEnd(-90, 0, 80), -1, 'past the threshold, leftwards');
  assert.equal(swipeEnd(90, 0, 80), 1, 'past the threshold, rightwards');
  assert.equal(swipeEnd(-40, 0, 80), 0, 'short and slow snaps back');
  assert.equal(swipeEnd(-40, -1.2, 80), -1, 'short but flicked still fires');
  assert.equal(swipeEnd(-10, -3, 80), 0, 'a fast twitch is still a twitch');
  assert.equal(swipeEnd(0, 0, 80), 0);

  console.log('swipeGesture.check: ok');
}

if (require.main === module) check();
