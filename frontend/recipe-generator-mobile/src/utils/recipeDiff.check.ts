/**
 * Self-check for the recipe version diff (BACKLOG.md 10.3). The whole value of the card is
 * that "make it vegan" names what moved — a diff that reports a swapped ingredient as one
 * removal plus one addition, or that calls an untouched recipe changed, is worse than no
 * diff at all.
 *
 * There is no test runner in this repo (see `AGENT.md`), so this is a plain assert script.
 * Run it:
 *
 *   npx tsc src/utils/recipeDiff.check.ts --outDir /tmp/rd --module commonjs \
 *     --target es2019 --moduleResolution node --skipLibCheck ; node /tmp/rd/utils/recipeDiff.check.js
 *
 * Nothing imports this file, so it never reaches a bundle; `npx tsc --noEmit` typechecks it.
 */
import { strict as assert } from 'assert';
import { RecipeDocument } from '../types';
import { diffRecipes, isEmptyDiff, diffSize } from './recipeDiff';

type Ing = RecipeDocument['ingredients'][number];

const ing = (item: string, quantity: number | null = null, unit: string | null = null): Ing =>
  ({ item, quantity, quantity_text: null, unit, section: null, optional: false });

const doc = (over: Partial<RecipeDocument> = {}): RecipeDocument => ({
  title: 'Lasagne',
  summary: null,
  language: 'en',
  tags: [],
  servings: 4,
  total_minutes: 60,
  ingredients: [ing('ricotta', 250, 'g'), ing('spinach', 200, 'g')],
  steps: [
    { sort_order: 1, step_text: 'Boil the pasta.' },
    { sort_order: 2, step_text: 'Layer it up.' },
  ],
  ...over,
});

export function check(): void {
  // An unchanged document is an empty diff — the card must render nothing, not a heading
  // over three empty lists.
  const same = diffRecipes(doc(), doc());
  assert.ok(isEmptyDiff(same), 'identical documents must diff to nothing');
  assert.equal(diffSize(same), 0);

  // The headline case: a swap is one *changed* line, not a removal plus an addition.
  const vegan = diffRecipes(
    doc(),
    doc({ ingredients: [ing('cashew cream', 250, 'g'), ing('spinach', 200, 'g')] })
  );
  assert.deepEqual(vegan.ingredients.added, ['250 g cashew cream']);
  assert.deepEqual(vegan.ingredients.removed, ['250 g ricotta']);
  assert.equal(vegan.ingredients.changed.length, 0, 'a different food is not a changed line');

  // Same food, different amount — that IS a changed line, and must not read as a swap.
  const halved = diffRecipes(
    doc(),
    doc({ ingredients: [ing('ricotta', 125, 'g'), ing('spinach', 100, 'g')], servings: 2 })
  );
  assert.equal(halved.ingredients.added.length, 0);
  assert.equal(halved.ingredients.removed.length, 0);
  assert.deepEqual(halved.ingredients.changed, [
    { from: '250 g ricotta', to: '125 g ricotta' },
    { from: '200 g spinach', to: '100 g spinach' },
  ]);
  assert.deepEqual(halved.fields, [{ key: 'servings', from: '4', to: '2' }]);

  // Case and surrounding space do not change the key: "Ricotta" coming back capitalised is
  // the same food, so at worst it is a changed line — never a removal plus an addition.
  const recased = diffRecipes(
    doc(),
    doc({ ingredients: [ing(' Ricotta ', 250, 'g'), ing('spinach', 200, 'g')] })
  );
  assert.equal(recased.ingredients.added.length, 0);
  assert.equal(recased.ingredients.removed.length, 0);

  // Steps: rewritten in place is a changed position; a longer list reports the count.
  const steps = diffRecipes(
    doc(),
    doc({
      steps: [
        { sort_order: 1, step_text: 'Boil the pasta in salted water.' },
        { sort_order: 2, step_text: 'Layer it up.' },
        { sort_order: 3, step_text: 'Bake for 30 minutes.' },
      ],
    })
  );
  assert.deepEqual(steps.steps.changed, [1]);
  assert.equal(steps.steps.added, 1);
  assert.equal(steps.steps.removed, 0);

  // A renamed recipe is a field change, and nothing else.
  const renamed = diffRecipes(doc(), doc({ title: 'Vegan Lasagne' }));
  assert.deepEqual(renamed.fields, [{ key: 'title', from: 'Lasagne', to: 'Vegan Lasagne' }]);
  assert.equal(diffSize(renamed), 1);

  console.log('recipeDiff.check: all assertions passed');
}

check();
