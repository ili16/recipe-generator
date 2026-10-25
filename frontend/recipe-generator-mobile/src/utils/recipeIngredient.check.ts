/**
 * Self-check for the servings scaler (BACKLOG 17.3). The rule that matters is the refusal:
 * a numeric quantity scales, a `quantity_text` one never does. "a pinch" must not become
 * "1.5 pinches" on a screen someone is cooking from.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/utils/recipeIngredient.check.ts --outDir /tmp/ri --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/ri/utils/recipeIngredient.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` still typechecks it.
 */
import { strict as assert } from 'assert';
import { fmtIngredient, scaleIngredient, servingScale, Ingredient } from './recipeIngredient';

const ing = (over: Partial<Ingredient>): Ingredient =>
  ({ item: 'onion', optional: false, ...over }) as Ingredient;

// --- servingScale: the factor, and its refusals ---
assert.equal(servingScale(6, 4), 1.5);
assert.equal(servingScale(2, 4), 0.5);
assert.equal(servingScale(6, null), 1, 'an unknown yield means as-written');
assert.equal(servingScale(null, 4), 1, 'an unknown target means as-written');
assert.equal(servingScale(0, 4), 1, 'cooking for nobody is not a scale factor');
assert.equal(servingScale(6, 0), 1, 'a recipe yielding nothing cannot be scaled from');

// --- scaleIngredient: numbers scale ---
assert.equal(scaleIngredient(ing({ quantity: 200, unit: 'g' }), 1.5).quantity, 300);
assert.equal(scaleIngredient(ing({ quantity: 1 }), 1.5).quantity, 1.5);

// --- scaleIngredient: words do not ---
const pinch = ing({ quantity_text: 'a pinch of', item: 'salt' });
assert.equal(scaleIngredient(pinch, 4).quantity_text, 'a pinch of');
assert.equal(scaleIngredient(pinch, 4).quantity, undefined, 'a worded amount gains no number');
assert.equal(fmtIngredient(scaleIngredient(pinch, 4), true), 'a pinch of salt');

// Factor 1 hands back the very same object, so an unscaled recipe renders unchanged.
const asWritten = ing({ quantity: 2 });
assert.ok(scaleIngredient(asWritten, 1) === asWritten);

// --- fmtIngredient: rendering ---
assert.equal(fmtIngredient(ing({ quantity: 250, unit: 'g', item: 'flour' })), '250 g flour');
assert.equal(fmtIngredient(ing({ quantity: 2 })), '2 onion');
// Floating point from a scale must not reach the screen: 0.1 * 3 is 0.30000000000000004.
assert.equal(fmtIngredient(scaleIngredient(ing({ quantity: 0.1, unit: 'kg' }), 3)), '0.3 kg onion');

// --- fmtIngredient: the gram hint ---
const half = scaleIngredient(ing({ quantity: 1, grams_per_unit: 150 }), 1.5);
assert.equal(fmtIngredient(half, true), '1.5 onion (~225 g)');
assert.equal(fmtIngredient(half), '1.5 onion', 'the hint is opt-in: the version diff must not grow one');
assert.equal(
  fmtIngredient(scaleIngredient(ing({ quantity: 2, grams_per_unit: 150 }), 1.5), true),
  '3 onion',
  'a whole number of onions needs no help',
);
assert.equal(
  fmtIngredient(scaleIngredient(ing({ quantity: 1, unit: 'g', grams_per_unit: 1 }), 1.5), true),
  '1.5 g onion',
  'a line already written by weight needs no hint',
);
assert.equal(
  fmtIngredient(scaleIngredient(ing({ quantity: 1 }), 1.5), true),
  '1.5 onion',
  'nothing knows what it weighs, so nothing is invented',
);

console.log('recipeIngredient.check: all assertions passed');
