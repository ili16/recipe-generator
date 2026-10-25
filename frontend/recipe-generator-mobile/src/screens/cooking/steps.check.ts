/**
 * Self-check for `groupSteps` — the piece that decides what one screen of cooking mode is.
 * Get it wrong in the folding direction and two unrelated steps get done at once; get it
 * wrong in the other and the parallelism the model found never reaches the cook.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/screens/cooking/steps.check.ts --outDir /tmp/sc --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/sc/screens/cooking/steps.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` still typechecks it.
 */
import { strict as assert } from 'assert';
import type { CookPhase } from '../../types';
import { formatCountdown, getStepIngredientIndices, groupSteps, groupSpokenText, phaseSummary, Ingredient, Step } from './steps';

type Spec = { phase?: CookPhase; group?: number; timer?: number };

function step(text: string, { phase, group, timer }: Spec = {}, order = 1): Step {
  return {
    sort_order: order,
    step_text: text,
    phase: phase ?? null,
    parallel_group: group ?? null,
    timer_seconds: timer ?? null,
  };
}

function steps(...specs: Array<[string, Spec?]>): Step[] {
  return specs.map(([text, spec], i) => step(text, spec, i + 1));
}

export function check(): void {
  // 1. The legacy case, and the one that must never regress: a recipe saved before the
  // cook flow existed has no phase and no group on any step, and must still walk one
  // step per screen with nothing new rendered.
  const legacy = steps(['Boil water'], ['Chop onion'], ['Fry it']);
  const l = groupSteps(legacy);
  assert.equal(l.length, 3);
  assert.deepEqual(l.map(g => g.steps.length), [1, 1, 1]);
  assert.deepEqual(l.map(g => g.phase), [null, null, null]);
  assert.deepEqual(l.map(g => g.timerSeconds), [null, null, null]);
  assert.deepEqual(l.map(g => g.indices), [[0], [1], [2]]);

  // 2. Two consecutive steps in the same group fold into one card.
  const pasta = groupSteps(steps(
    ['Boil a large pot of salted water', { phase: 'cook', group: 1, timer: 600 }],
    ['Dice the onion and pepper', { phase: 'prep', group: 1, timer: 300 }],
    ['Drain and toss with the sauce', { phase: 'cook', timer: 120 }],
  ));
  assert.equal(pasta.length, 2);
  assert.deepEqual(pasta[0].steps.map(s => s.step_text.slice(0, 4)), ['Boil', 'Dice']);
  assert.deepEqual(pasta[0].indices, [0, 1]);
  assert.deepEqual(pasta[1].indices, [2]);

  // 3. The group's time is the longest track, not the sum — two things at once take as
  // long as the slower one. 600 and 300 is a ten-minute block, not a fifteen-minute one.
  assert.equal(pasta[0].timerSeconds, 600);

  // 4. The group takes the phase of its first step that has one.
  assert.equal(pasta[0].phase, 'cook');
  assert.equal(groupSteps(steps(
    ['Wait for the oven', { group: 2 }],
    ['Grate the cheese', { phase: 'prep', group: 2 }],
  ))[0].phase, 'prep');

  // 5. The same group number reused later is a SEPARATE block of parallel work, not a
  // continuation — only consecutive steps fold.
  const reused = groupSteps(steps(
    ['Boil water', { group: 1 }],
    ['Chop', { group: 1 }],
    ['Sear the meat', {}],
    ['Reduce the sauce', { group: 1 }],
    ['Warm the plates', { group: 1 }],
  ));
  assert.equal(reused.length, 3);
  assert.deepEqual(reused.map(g => g.indices), [[0, 1], [2], [3, 4]]);

  // 6. A group of one renders as an ordinary step — no special casing downstream.
  const solo = groupSteps(steps(['Rest the meat', { phase: 'wait', group: 7, timer: 600 }]));
  assert.equal(solo.length, 1);
  assert.equal(solo[0].steps.length, 1);
  assert.equal(solo[0].timerSeconds, 600);

  // 7. Empty in, empty out.
  assert.deepEqual(groupSteps([]), []);

  // 8. Spoken text: a lone step is read as written; a parallel block is announced and
  // numbered, or a hands-free listener hears two instructions with no idea they overlap.
  // The announcement itself comes from the catalogs; the key stands in for it here.
  const key = (k: string) => k;
  assert.equal(groupSpokenText(solo[0], key), 'Rest the meat');
  assert.equal(
    groupSpokenText(pasta[0], key),
    'cooking.atTheSameTime 1. Boil a large pot of salted water 2. Dice the onion and pepper',
  );

  // 9. The overview summary counts a parallel block once, at its longest track, and drops
  // phases the recipe does not have.
  const summary = phaseSummary(pasta);
  assert.deepEqual(summary.map(p => p.phase), ['cook']);
  assert.equal(summary[0].groups, 2);
  assert.equal(summary[0].seconds, 720); // 600 (the block) + 120, not 1020
  assert.deepEqual(phaseSummary(l), []); // no phases at all → nothing to summarise

  // 10. Step ingredients come back as indices into the recipe's list — what a tick in
  // cooking mode is keyed on. DB indices win and out-of-range ones are dropped; with
  // none, the text fallback matches on words longer than three characters.
  const ings = [
    { item: 'onion', quantity: 1, unit: null },
    { item: 'olive oil', quantity: 2, unit: 'tbsp' },
  ] as unknown as Ingredient[];
  const fromDb = { ...step('Anything at all'), ingredient_indices: [1, 9] } as Step;
  assert.deepEqual(getStepIngredientIndices(fromDb, ings), [1]);
  assert.deepEqual(getStepIngredientIndices(step('Dice the onion'), ings), [0]);
  assert.deepEqual(getStepIngredientIndices(step('Rest the meat'), ings), []);

  // 11. The live countdown: mm:ss, zero-padded seconds, clamped at zero rather than
  // counting into negative time when the tick lands late.
  assert.equal(formatCountdown(600_000), '10:00');
  assert.equal(formatCountdown(65_000), '1:05');
  assert.equal(formatCountdown(1), '0:01');
  assert.equal(formatCountdown(-5_000), '0:00');

  console.log('steps: all 11 checks pass');
}

check();
