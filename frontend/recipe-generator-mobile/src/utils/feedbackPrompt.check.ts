/**
 * Self-check for the pulse-prompt eligibility rules (BACKLOG 9.16). This is the logic that
 * decides whether to interrupt someone, and every failure mode is invisible: ask too early
 * and it is noise, forget a clock and it becomes the app that nags, and both look fine in
 * a screenshot.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/utils/feedbackPrompt.check.ts --outDir /tmp/fp --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/fp/feedbackPrompt.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` still typechecks it.
 */
import { strict as assert } from 'assert';
import {
  shouldAsk, emptyState, FeedbackPromptState,
  MIN_COOKS, DISMISS_COOLDOWN_DAYS, MAX_ASKS,
} from './feedbackPrompt';

const DAY = 24 * 60 * 60 * 1000;
const NOW = 1_800_000_000_000; // a fixed "now" so the cases read as arithmetic

const state = (over: Partial<FeedbackPromptState>): FeedbackPromptState => ({ ...emptyState, ...over });

export function check(): void {
  // A brand-new install is not asked anything.
  assert.equal(shouldAsk(emptyState, NOW), false, 'fresh install must not be asked');

  // Below the milestone, however long they have had the app.
  assert.equal(shouldAsk(state({ cooks: MIN_COOKS - 1 }), NOW), false, 'under MIN_COOKS');

  // At the milestone, never asked before: this is the one case that asks.
  assert.equal(shouldAsk(state({ cooks: MIN_COOKS }), NOW), true, 'at MIN_COOKS, first ask');
  assert.equal(shouldAsk(state({ cooks: 99 }), NOW), true, 'well past MIN_COOKS');

  // Dismissed: quiet for DISMISS_COOLDOWN_DAYS, then eligible again.
  const dismissed = state({ cooks: MIN_COOKS, asks: 1, lastAskedAt: NOW });
  assert.equal(shouldAsk(dismissed, NOW), false, 'same instant as the dismissal');
  assert.equal(shouldAsk(dismissed, NOW + DISMISS_COOLDOWN_DAYS * DAY - 1), false, 'one ms inside the cooldown');
  assert.equal(shouldAsk(dismissed, NOW + DISMISS_COOLDOWN_DAYS * DAY), true, 'exactly at the cooldown boundary');

  // The ceiling: three asks and we stop, cooldown or not.
  const spent = state({ cooks: MIN_COOKS, asks: MAX_ASKS, lastAskedAt: NOW - 365 * DAY });
  assert.equal(shouldAsk(spent, NOW), false, 'MAX_ASKS is a hard ceiling');

  // Answering ends it for good — the long clock, not the short one.
  assert.equal(shouldAsk(state({ cooks: 99, answered: true }), NOW + 365 * DAY), false, 'answered is permanent');

  // So does an explicit opt-out, and it beats everything else in the struct.
  assert.equal(shouldAsk(state({ cooks: 99, optedOut: true }), NOW + 365 * DAY), false, 'opt-out is permanent');

  // Opt-out wins even if the state somehow also says answered/eligible.
  assert.equal(shouldAsk(state({ cooks: 99, asks: 0, optedOut: true, answered: false }), NOW), false, 'opt-out beats eligibility');

  // A lastAskedAt of 0 means "never asked" and must not be read as an ancient timestamp
  // that is trivially outside the cooldown — it is the same eligible case as the first ask.
  assert.equal(shouldAsk(state({ cooks: MIN_COOKS, lastAskedAt: 0 }), NOW), true, 'lastAskedAt 0 = never asked');

  console.log('feedbackPrompt.check: all assertions passed');
}

check();
